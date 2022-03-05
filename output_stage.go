package main

import (
	"context"
	"encoding/binary"
	"fmt"
	"os"
	"sync"
	"time"
)

const (
	_DEBUG_FILE_OUTPUT = "example.wav"
)

func newOutputStage(ctx context.Context, cfg *config) (*outputState, error) {
	rate, err := cfg.sampleRate()
	stageCtx, cancel := context.WithCancel(ctx)
	return &outputState{
		ctx: stageCtx, cancel: cancel,
		rate: rate, pad: cfg.AudioOutput.PadGaps,
	}, err
}

type outputState struct {
	ctx    context.Context
	cancel context.CancelFunc
	rate   int
	pad    bool
}

func (o *outputState) stop() {
	o.cancel()
}

func (o *outputState) routine(wg *sync.WaitGroup, audioIn chan []int16) (func(), error) {
	fileOutput, err := os.Create(_DEBUG_FILE_OUTPUT)
	if err != nil {
		return nil, err
	}

	return func() {
		defer func() {
			fmt.Fprintf(os.Stderr, "Returning from outputRoutine\n")
			fileOutput.Close()
			wg.Done()
		}()

		if o.pad {
			startTime := time.Now()
			var samples, samplesNow int64
			ticker := time.NewTicker(time.Millisecond * 10)
			defer ticker.Stop()
			for {
				select {
				case <-o.ctx.Done():
					fmt.Println("[output] context cancelled, finishing output.")
					return
				case buf := <-audioIn:
					samples += int64(len(buf))
					if err := binary.Write(fileOutput, binary.LittleEndian, buf); err != nil {
						fmt.Fprintf(os.Stderr, "output write error: %s\n", err)
					}
				case <-ticker.C:
					samplesNow = int64((time.Since(startTime) * time.Duration(o.rate)) / time.Second)
					if samplesNow < samples {
						continue
					}

					buf := make([]int16, samplesNow-samples)
					if err := binary.Write(fileOutput, binary.LittleEndian, buf); err != nil {
						fmt.Fprintf(os.Stderr, "output write error: %s\n", err)
					}
					samples = samplesNow
				}
			}
		} else {
			for {
				select {
				case <-o.ctx.Done():
					fmt.Println("[output] context cancelled, finishing output.")
					return
				case buf := <-audioIn:
					if err := binary.Write(fileOutput, binary.LittleEndian, buf); err != nil {
						fmt.Fprintf(os.Stderr, "output write error: %s\n", err)
					}
				}
			}
		}
	}, nil
}
