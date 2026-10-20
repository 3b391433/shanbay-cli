package main

import (
	"errors"
	"io"
	"os"
	"testing"
	"time"
)

func TestInputSavesWhileWaitingAndInterrupts(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	input := newLineInput(reader)
	defer input.Close()
	ticks := make(chan time.Time, 1)
	signals := make(chan os.Signal, 1)
	saved := make(chan struct{})
	result := make(chan bool, 1)
	go func() {
		_, ok, _ := input.Next(ticks, signals, func() error { close(saved); return nil })
		result <- ok
	}()
	ticks <- time.Now()
	select {
	case <-saved:
	case <-time.After(time.Second):
		t.Fatal("save blocked behind stdin")
	}
	signals <- os.Interrupt
	select {
	case ok := <-result:
		if ok {
			t.Fatal("interrupt accepted as input")
		}
	case <-time.After(time.Second):
		t.Fatal("interrupt blocked behind stdin")
	}
}

func TestInputReturnsSaveFailure(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	input := newLineInput(reader)
	defer input.Close()
	ticks := make(chan time.Time, 1)
	ticks <- time.Now()
	failure := errors.New("save failed")
	_, ok, err := input.Next(ticks, nil, func() error { return failure })
	if ok || !errors.Is(err, failure) {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
}
