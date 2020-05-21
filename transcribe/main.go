// Copyright 2020 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// This package provides an experimental app that transcribes a stream of audio
// to text using Google Cloud Speech-to-Text StreamingRecognize.
// See https://cloud.google.com/speech-to-text/docs/basics#streaming-recognition.
// Input audio data is consumed from a Redis queue, and rolling transcription fragments
// are output to another Redis queue.
// Most recently received audio data is replayed upon errors, to facilitate recovery.
// The listens on an HTTP port to start/stop transcriptions. This facilitates deployment
// in a leader election pattern; multiple transcriber instances can be redundantly deployed,
// only the leader is "started" to perform transcriptions.
package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	speech "cloud.google.com/go/speech/apiv1p1beta1"
	redis "github.com/go-redis/redis/v7"
	speechpb "google.golang.org/genproto/googleapis/cloud/speech/v1p1beta1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/klog"
)

var (
	electionPort = flag.Int("electionPort", 4040,
		"Listen at this port for leader election updates. Set to zero to disable leader election")
	redisHost          = flag.String("redisHost", "localhost", "Redis host IP")
	audioQueue         = flag.String("audioQueue", "liveq", "Redis key for input audio data")
	transcriptionQueue = flag.String("transcriptionQueue", "transcriptions", "Redis key for output transcriptions")
	recoveryQueue      = flag.String("recoveryQueue", "recoverq", "Redis key for recent audio data used for recovery")
	recoveryRetain     = flag.Duration("recoveryRetainLast", 3*time.Second, "Retain this duration of audio, replayed during recovery")
	recoveryExpiry     = flag.Duration("recoveryExpiry", 30*time.Second, "Expire data in recovery queue after this time")
	flushTimeout       = flag.Duration("flushTimeout", 3000*time.Millisecond, "Emit any pending transcriptions after this time")
	pendingWordCount   = flag.Int("pendingWordCount", 4, "Treat last N transcribed words as pending")
	sampleRate         = flag.Int("sampleRate", 16000, "Sample rate (Hz)")
	channels           = flag.Int("channels", 1, "Number of audio channels")
	lang               = flag.String("lang", "en-US", "Transcription language code")
	phrases            = flag.String("phrases", "", "Comma-separated list of phrase hints for Speech API. Phrases with spaces should be quoted")

	redisClient        *redis.Client
	recoveryRetainSize = int64(*recoveryRetain / (100 * time.Millisecond)) // each audio element ~100ms
	latestTranscript   string
	lastIndex          = 0
	pending            []string
)

func main() {
	flag.Parse()
	klog.InitFlags(nil)

	redisClient = redis.NewClient(&redis.Options{
		Addr:        *redisHost + ":6379",
		Password:    "", // no password set
		DB:          0,  // use default DB
		DialTimeout: 3 * time.Second,
		ReadTimeout: 4 * time.Second,
	})

	speechClient, err := speech.NewClient(context.Background())
	if err != nil {
		klog.Fatal(err)
	}

	contextPhrases := []string{}
	if *phrases != "" {
		contextPhrases = strings.Split(*phrases, ",")
		klog.Infof("Supplying %d phrase hints: %+q", len(contextPhrases), contextPhrases)
	}
	streamingConfig := speechpb.StreamingRecognitionConfig{
		Config: &speechpb.RecognitionConfig{
			Encoding:                   speechpb.RecognitionConfig_LINEAR16,
			SampleRateHertz:            int32(*sampleRate),
			AudioChannelCount:          int32(*channels),
			LanguageCode:               *lang,
			EnableAutomaticPunctuation: true,
			SpeechContexts: []*speechpb.SpeechContext{
				{Phrases: contextPhrases},
			},
		},
		InterimResults: true,
	}

	// If a port is defined, listen there for callbacks from leader election.
	// Only send audio to Speech while we're the leader.
	if *electionPort > 0 {
		var ctx context.Context
		var cancel context.CancelFunc
		addr := fmt.Sprintf(":%d", *electionPort)
		server := &http.Server{Addr: addr}

		// Listen for interrupt. If so, cancel the context to stop transcriptions,
		// then shutdown the HTTP server, which will end the main thread.
		ch := make(chan os.Signal, 1)
		signal.Notify(ch, os.Interrupt, syscall.SIGTERM)
		go func() {
			<-ch
			if cancel != nil {
				cancel()
			}
			klog.Info("Received termination, stopping transciptions")
			if err := server.Shutdown(context.TODO()); err != nil {
				klog.Fatalf("Error shutting down HTTP server: %v", err)
			}
		}()

		isLeader := false
		webHandler := func(res http.ResponseWriter, req *http.Request) {
			// If we're elected as leader, start a goroutine to read audio from
			// Redis and stream to Cloud Speech
			if strings.Contains(req.URL.Path, "start") {
				if !isLeader {
					isLeader = true
					klog.Infof("I became the leader! Starting goroutine to send audio")
					ctx, cancel = context.WithCancel(context.Background())
					go sendAudio(ctx, speechClient, streamingConfig)
				}
			}
			// If we stop being the leader, stop sending audio
			if strings.Contains(req.URL.Path, "stop") {
				if isLeader {
					isLeader = false
					klog.Infof("I stopped being the leader!")
					cancel()
				}
			}
			res.WriteHeader(http.StatusOK)
		}
		http.HandleFunc("/", webHandler)
		klog.Infof("Starting leader election listener at port %s", addr)
		// blocks
		server.ListenAndServe()
	} else {
		// Standalone execution, testing
		klog.Info("Not doing leader election")
		sendAudio(context.Background(), speechClient, streamingConfig)
	}
}

// Consumes queued audio data from Redis, and sends to Speech.
// Uses a reliable queueing approach such that the most recently received audio data
// is also temporarily stored in a parallel 'recovery' queue.
// See https://redis.io/commands/rpoplpush#pattern-reliable-queue.
// At startup, or in the case of Redis errors, data in the recovery queue is replayed first.
func sendAudio(ctx context.Context, speechClient *speech.Client, config speechpb.StreamingRecognitionConfig) {
	var stream speechpb.Speech_StreamingRecognizeClient
	receiveChan := make(chan bool)
	doRecovery := redisClient.Exists(*recoveryQueue).Val() > 0
	replaying := false

	for {
		select {
		case <-ctx.Done():
			klog.Infof("Context cancelled, exiting sender loop")
			return
		case _, ok := <-receiveChan:
			if !ok && stream != nil {
				klog.Info("receive channel closed, resetting stream")
				stream = nil
				receiveChan = make(chan bool)
				resetIndex()
				continue
			}
		default:
			// Process audio
		}

		var result string
		var err error
		if doRecovery {
			result, err = redisClient.RPop(*recoveryQueue).Result()
			if err == redis.Nil { // no more recovery data
				doRecovery = false
				replaying = false
				continue
			}
			if err == nil && !replaying {
				replaying = true
				emit("[REPLAY]")
			}
		} else {
			// Blocking pop audio data off the queue, and push onto recovery queue.
			// Timeout occasionally for hygiene.
			result, err = redisClient.BRPopLPush(*audioQueue, *recoveryQueue, 5*time.Second).Result()
			if err == redis.Nil { // pop timeout
				continue
			}
			// Temporarily retain the last few seconds of recent audio
			if err == nil {
				redisClient.LTrim(*recoveryQueue, 0, recoveryRetainSize)
				redisClient.Expire(*recoveryQueue, *recoveryExpiry)
			}
		}
		if err != nil && err != redis.Nil {
			klog.Errorf("Could not read from Redis: %v", err)
			doRecovery = true
			continue
		}

		// Establish bi-directional connection to Cloud Speech and
		// start a go routine to listen for responses
		if stream == nil {
			stream = initStreamingRequest(ctx, speechClient, config)
			go receiveResponses(stream, receiveChan)
			emit("[NEW STREAM]")
		}

		// Send audio, transcription responses received asynchronously
		decoded, _ := base64.StdEncoding.DecodeString(result)
		sendErr := stream.Send(&speechpb.StreamingRecognizeRequest{
			StreamingRequest: &speechpb.StreamingRecognizeRequest_AudioContent{
				AudioContent: decoded,
			},
		})
		if sendErr != nil {
			// Expected - if stream has been closed (e.g. timeout)
			if sendErr == io.EOF {
				continue
			}
			klog.Errorf("Could not send audio: %v", sendErr)
		}
	}
}

// Consumes StreamingResponses from Speech.
func receiveResponses(stream speechpb.Speech_StreamingRecognizeClient, receiveChan chan bool) {
	// Indicate that we're no longer listening for responses
	defer close(receiveChan)

	// If no results received from Speech for some period, emit any pending transcriptions
	timer := time.NewTimer(*flushTimeout)
	go func() {
		<-timer.C
		flush()
	}()
	defer timer.Stop()

	// Consume streaming responses from Speech
	for {
		resp, err := stream.Recv()
		if err != nil {
			// Context cancelled - expected, e.g. stopped being leader
			if status.Code(err) == codes.Canceled {
				return
			}
			klog.Errorf("Cannot stream results: %v", err)
			return
		}
		if err := resp.Error; err != nil {
			// Timeout - expected, when no audio sent for a time
			if status.FromProto(err).Code() == codes.OutOfRange {
				klog.Info("Timeout from API; closing connection")
				return
			}
			klog.Errorf("Could not recognize: %v", err)
			return
		}
		// If nothing received for a time, stop receiving
		if !timer.Stop() {
			return
		}
		// Ok, we have a valid response from Speech.
		timer.Reset(*flushTimeout)
		processResponses(*resp)
	}
}

// Handles transcription results.
// Broadly speaking, the goal is to emit 1-3 transcribed words at a time.
// An index is maintained to determine last emitted words.
// Emits a delimited string with 3 parts:
// 'steady' - these words are assumed final (won't change)
// 'pending' - interim transcriptions often evolve as Speech gets more data,
// 		so the most recently transcribed words should be treated with caution
// 'unstable' - low-stability transcription alternatives
func processResponses(resp speechpb.StreamingRecognizeResponse) {
	if len(resp.Results) == 0 {
		return
	}
	result := resp.Results[0]
	alternative := result.Alternatives[0]
	latestTranscript = alternative.Transcript
	elements := strings.Split(alternative.Transcript, " ")
	length := len(elements)

	// Speech will not further update this transcription; output it
	if result.GetIsFinal() || alternative.GetConfidence() > 0 {
		klog.Info("Final result! Resetting")
		final := elements[lastIndex:]
		emit(strings.Join(final, " "))
		resetIndex()
		return
	}
	if result.Stability < 0.75 {
		klog.Infof("Ignoring low stability result (%v): %s", resp.Results[0].Stability,
			resp.Results[0].Alternatives[0].Transcript)
		return
	}

	// Unstable, speculative transcriptions (very likley to change)
	unstable := ""
	if len(resp.Results) > 1 {
		unstable = resp.Results[1].Alternatives[0].Transcript
	}
	// Treat last N words as pending.
	// Treat delta between last and current index as steady
	if length < *pendingWordCount {
		lastIndex = 0
		pending = elements
		emitStages([]string{}, pending, unstable)
	} else if lastIndex < length-*pendingWordCount {
		steady := elements[lastIndex:(length - *pendingWordCount)]
		lastIndex += len(steady)
		pending = elements[lastIndex:]
		emitStages(steady, pending, unstable)
	}
}

func initStreamingRequest(ctx context.Context, client *speech.Client, config speechpb.StreamingRecognitionConfig) speechpb.Speech_StreamingRecognizeClient {
	stream, err := client.StreamingRecognize(ctx)
	if err != nil {
		klog.Fatal(err)
	}
	// Send the initial configuration message.
	if err := stream.Send(&speechpb.StreamingRecognizeRequest{
		StreamingRequest: &speechpb.StreamingRecognizeRequest_StreamingConfig{
			StreamingConfig: &config,
		},
	}); err != nil {
		klog.Errorf("Error sending initial config message: %v", err)
		return nil
	}
	klog.Info("Initialised new connection to Speech API")
	return stream
}

func emitStages(steady []string, pending []string, unstable string) {
	// Concatenate the different segments, let client decide how to display
	msg := fmt.Sprintf("%s|%s|%s", strings.Join(steady, " "),
		strings.Join(pending, " "), unstable)
	emit(msg)
}

func emit(msg string) {
	klog.Info(msg)
	redisClient.LPush(*transcriptionQueue, msg)
}

func flush() {
	msg := ""
	if pending != nil {
		msg += strings.Join(pending, " ")
	}
	if msg != "" {
		klog.Info("Flushing...")
		emit("[FLUSH] " + msg)
	}
	resetIndex()
}

func resetIndex() {
	lastIndex = 0
	pending = nil
}

// Debugging
func logResponses(resp speechpb.StreamingRecognizeResponse) {
	for _, result := range resp.Results {
		klog.Infof("Result: %+v\n", result)
	}
}
