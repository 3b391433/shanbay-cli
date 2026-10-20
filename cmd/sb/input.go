package main

import (
	"bufio"
	"io"
	"os"
	"time"

	"github.com/muesli/cancelreader"
)

// lineInput keeps the study loop responsive to save timers and interrupts while
// the user is thinking. Only the study goroutine owns grades and queues.
type lineInput struct {
	lines   chan string
	done    chan struct{}
	err     error
	reader  cancelreader.CancelReader
	stopped chan struct{}
}

func newLineInput(r io.Reader) *lineInput {
	in := &lineInput{lines: make(chan string), done: make(chan struct{}), stopped: make(chan struct{})}
	// A terminal reader must be canceled before a possible re-login can read
	// stdin again. Regular redirected files may not support poll cancellation.
	if reader, err := cancelreader.NewReader(r); err == nil {
		in.reader, r = reader, reader
	}
	go func() {
		defer close(in.stopped)
		defer close(in.lines)
		scanner := bufio.NewScanner(r)
		for scanner.Scan() {
			select {
			case in.lines <- scanner.Text():
			case <-in.done:
				return
			}
		}
		in.err = scanner.Err()
	}()
	return in
}

func (in *lineInput) Close() {
	close(in.done)
	if in.reader != nil {
		if in.reader.Cancel() {
			<-in.stopped
		}
		_ = in.reader.Close()
	}
}

func (in *lineInput) Next(ticks <-chan time.Time, signals <-chan os.Signal, save func() error) (string, bool, error) {
	for {
		select {
		case text, ok := <-in.lines:
			if !ok {
				return "", false, in.err
			}
			return text, true, nil
		case <-ticks:
			if err := save(); err != nil {
				return "", false, err
			}
		case <-signals:
			return "", false, nil
		}
	}
}
