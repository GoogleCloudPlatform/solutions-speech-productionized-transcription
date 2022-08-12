package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"k8s.io/klog/v2"
)

var (
	ingestPort = flag.Int("ingestPort", 9000, "excepting port number")
)

func main() {
	var ctx context.Context
	var cancel context.CancelFunc

	ch := make(chan os.Signal, 1)
	ctx, cancel = context.WithCancel(context.Background())

	go func(p int, ctx context.Context) {

		for {
			select {
			case <-ctx.Done():
				klog.Info("os interrupt")
				return
			default:
				fmt.Println(*ingestPort)
				//
			}
		}
	}(*ingestPort, ctx)

	_ = ch
	time.Sleep(1 * time.Second)
	cancel()
	time.Sleep(5 * time.Second)

}
