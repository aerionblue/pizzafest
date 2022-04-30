// Package tipfile reads donations (monetary tips) from a text file.
//
// The file should contain one line per donation, with the following fields,
// delimited by semicolons:
//     * An arbitrary unique ID.
//     * The amount of the tip, in US cents.
//     * The username of the tipper.
//     * Any message supplied by the tipper.
//
// This can be used as an interface other programs capable of receiving
// donation alerts from an external source. (E.g., if you want to use
// StreamElements and don't want to wait for the maintainer of this code to
// actually go integrate correctly with the StreamElements API.)
package tipfile

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aerionblue/pizzafest/donation"
	retry "github.com/avast/retry-go"
	"github.com/fsnotify/fsnotify"
)

const logLineDelimiter = ";"

type Watcher struct {
	*fsnotify.Watcher
	// Channel on which new incoming donation events are reported.
	C <-chan donation.Event
	// Channel that, when closed, disposes of the fsnotify.Watcher.
	done chan struct{}

	mu sync.Mutex
	// Set of all donation IDs that have already been processed.
	processedIDs map[string]bool
}

func NewWatcher(path string, twitchChannel string) (*Watcher, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	donationChan := make(chan donation.Event, 100)
	w := &Watcher{
		Watcher:      watcher,
		C:            donationChan,
		processedIDs: make(map[string]bool),
	}

	// Initialize w.processedIDs with the lines that are already in the file.
	if _, err := w.processTipLog(path); err != nil {
		return nil, fmt.Errorf("error reading tip file: %v", err)
	}
	log.Printf("read %d entries from %s", len(w.processedIDs), path)

	go func() {
		defer close(donationChan)
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op != fsnotify.Write {
					continue
				}
				// Wait a moment to give the writer a chance to close the file.
				time.Sleep(500 * time.Millisecond)
				// TODO(aerion): Don't re-read the entire file every time.
				newEvents, err := w.processTipLog(event.Name)
				if err != nil {
					log.Printf("ERROR reading donation tip log: %v", err)
					continue
				}
				for _, ev := range newEvents {
					d := donation.Event{
						Owner:   ev.Username,
						Channel: twitchChannel,
						Cash:    donation.CentsValue(ev.Cents),
						Message: ev.Message,
					}
					donationChan <- d
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("ERROR watching donation tip log: %v", err)
			}
		}
	}()

	err = watcher.Add(path)
	if err != nil {
		return nil, err
	}
	return w, nil
}

// Close disposes of the Watcher.
func (w *Watcher) Close() error {
	return w.Watcher.Close()
}

func (w *Watcher) processTipLog(path string) ([]logEntry, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	var newEntries []logEntry

	var f *os.File
	// Try opening the file a few times, in case the file is still being held
	// open by the writing process (which has happened, in practice).
	err := retry.Do(
		func() error {
			var err error
			f, err = os.Open(path)
			return err
		},
		retry.Delay(1*time.Second),
		retry.Attempts(3),
	)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		entry, err := parseTipLogLine(scanner.Text())
		if err != nil {
			log.Printf("error parsing line: %v", err)
			continue
		}
		if isOld := w.processedIDs[entry.ID]; !isOld {
			w.processedIDs[entry.ID] = true
			newEntries = append(newEntries, entry)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return newEntries, nil
}

// logEntry represents one line of the tip text file. Each line describes one donation.
type logEntry struct {
	ID       string
	Cents    int
	Username string
	Message  string
}

func parseTipLogLine(line string) (logEntry, error) {
	tokens := strings.SplitN(line, logLineDelimiter, 4)
	for len(tokens) < 4 {
		tokens = append(tokens, "")
	}
	cents, err := strconv.Atoi(tokens[1])
	if err != nil {
		return logEntry{}, fmt.Errorf("error parsing donation amount %q: %v", tokens[1], err)
	}
	return logEntry{
		ID:    tokens[0],
		Cents: cents,
		// It's worth noting that our motivation for putting the username this
		// late in the line is that we can't necessarily prevent the donator
		// from putting the delimiter character in their username. If that
		// happens, we'll still get the important information (the dollar
		// amount), and we'll just deal with possibly losing the rest.
		Username: tokens[2],
		Message:  tokens[3],
	}, nil
}
