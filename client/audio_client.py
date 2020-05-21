# Copyright 2020 Google LLC
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
# https://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

import argparse
from datetime import datetime
import wave

import eventlet
import socketio

eventlet.monkey_patch()

parser = argparse.ArgumentParser()
parser.add_argument('--targetip', default='localhost:8080')
parser.add_argument('--file', default='humptydumpty.wav')
args = parser.parse_args()

sio = socketio.Client(reconnection_delay=1, reconnection_delay_max=1,
                      randomization_factor=0, logger=False)


@sio.event
def connect():
    print('Socket connected at %s' % datetime.utcnow())


@sio.event
def disconnect():
    print('Socket disconnected at %s' % datetime.utcnow())


@sio.on('pod_id')
def pod_id(msg):
    print('Connected to pod: %s' % msg)


def stream_file(filename):
    """Streams the supplied WAV file via socketio, continuously replaying."""
    wf = wave.open(filename, 'rb')
    # read in ~100ms chunks
    chunk = int(wf.getframerate() / 10)
    data = wf.readframes(chunk)
    while True:
        try:
            while sio.connected:
                if data != '' and len(data) != 0:
                    sio.emit('data', data)
                    # sleep for the duration of the audio chunk
                    # to mimic real time playback
                    sio.sleep(0.1)
                    data = wf.readframes(chunk)
                else:
                    print('EOF, pausing')
                    sio.sleep(0.5)
                    wf = wave.open(filename, 'rb')
                    data = wf.readframes(chunk)
                    print('restarting playback')
            sio.sleep(0.2)
        except socketio.exceptions.ConnectionError as err:
            print('Connection error: %s! Retrying at %s' %
                  (err, datetime.utcnow()))
        except KeyboardInterrupt:
            return


if __name__ == '__main__':
    try:
        url = 'http://' + args.targetip
        sio.connect(url)
        stream_file(args.file)
    except KeyboardInterrupt:
        pass
