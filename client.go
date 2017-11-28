/* This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at http://mozilla.org/MPL/2.0/. */

package sse

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"log"
	"net/http"
	"net/http/httptrace"
	"sync"
)

var (
	headerID    = []byte("id:")
	headerData  = []byte("data:")
	headerEvent = []byte("event:")
	headerError = []byte("error:")
)

// Client handles an incoming server stream
type Client struct {
	URL            string
	Connection     *http.Client
	Headers        map[string]string
	EncodingBase64 bool
	EventID        string
	connectedChan  chan struct{}
	lock           *sync.Mutex
}

// NewClient creates a new client
func NewClient(url string) *Client {
	c := &Client{
		URL:           url,
		Connection:    &http.Client{},
		Headers:       make(map[string]string),
		connectedChan: make(chan struct{}),
		lock:          &sync.Mutex{},
	}
	return c
}

// WaitForConnect waits for the first subscribe call to be fully connected
// if the context is cancelled before the connection finished, an error is returned.
func (c *Client) WaitForConnect(ctx context.Context) error {
	select {
	case <-c.connectedChan:
		return nil
	case <-ctx.Done():
		return errors.New("context was cancelled before connect finished")
	}
}

func (c *Client) connected() {
	c.lock.Lock()
	defer c.lock.Unlock()
	select {
	// Channel closed already
	case <-c.connectedChan:
		return
	default:
		close(c.connectedChan)
	}
}

// SubscribeContext to a data stream, with a cancelable context
func (c *Client) SubscribeContext(ctx context.Context, stream string, handler func(msg *Event)) error {
	resp, err := c.request(ctx, stream)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	reader := bufio.NewReader(resp.Body)

	for {
		// Read each new line and process the type of event
		line, err := reader.ReadBytes('\n')
		if err != nil {
			return err
		}
		msg := c.processEvent(line)
		if msg != nil {
			handler(msg)
		}
	}
}

// SubscribeChanContext sends all events to the provided channel, with a cancelable context
func (c *Client) SubscribeChanContext(ctx context.Context, stream string, ch chan *Event) error {
	resp, err := c.request(ctx, stream)
	if err != nil {
		close(ch)
		return err
	}

	if resp.StatusCode != 200 {
		close(ch)
		return errors.New("could not connect to stream")
	}

	reader := bufio.NewReader(resp.Body)

	go func() {
		for {
			// Read each new line and process the type of event
			line, err := reader.ReadBytes('\n')
			if err != nil {
				resp.Body.Close()
				close(ch)
				return
			}
			msg := c.processEvent(line)
			if msg != nil {
				ch <- msg
			}
		}
	}()

	return nil
}

// Subscribe to a data stream
func (c *Client) Subscribe(stream string, handler func(msg *Event)) error {
	return c.SubscribeContext(context.Background(), stream, handler)
}

// SubscribeChan sends all events to the provided channel
func (c *Client) SubscribeChan(stream string, ch chan *Event) error {
	return c.SubscribeChanContext(context.Background(), stream, ch)
}

func (c *Client) request(ctx context.Context, stream string) (*http.Response, error) {
	req, err := http.NewRequest("GET", c.URL, nil)
	if err != nil {
		return nil, err
	}
	trace := &httptrace.ClientTrace{
		WroteRequest: func(info httptrace.WroteRequestInfo) {
			if info.Err == nil {
				c.connected()
			}
		},
	}
	req = req.WithContext(httptrace.WithClientTrace(ctx, trace))

	// Setup request, specify stream to connect to
	query := req.URL.Query()
	query.Add("stream", stream)
	req.URL.RawQuery = query.Encode()

	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Connection", "keep-alive")

	if c.EventID != "" {
		req.Header.Set("Last-Event-ID", c.EventID)
	}

	// Add user specified headers
	for k, v := range c.Headers {
		req.Header.Set(k, v)
	}

	resp, err := c.Connection.Do(req)
	return resp, err
}

func (c *Client) processEvent(msg []byte) *Event {
	var e Event

	switch h := msg; {
	case bytes.Contains(h, headerID):
		e.ID = trimHeader(len(headerID), msg)
	case bytes.Contains(h, headerData):
		e.Data = trimHeader(len(headerData), msg)
	case bytes.Contains(h, headerEvent):
		e.Event = trimHeader(len(headerEvent), msg)
	case bytes.Contains(h, headerError):
		e.Error = trimHeader(len(headerError), msg)
	default:
		return nil
	}

	if len(e.Data) > 0 && c.EncodingBase64 {
		buf := make([]byte, base64.StdEncoding.DecodedLen(len(e.Data)))

		_, err := base64.StdEncoding.Decode(buf, e.Data)
		if err != nil {
			log.Println(err)
		}

		e.Data = buf
	}

	return &e
}

func trimHeader(size int, data []byte) []byte {
	data = data[size:]
	// Remove optional leading whitespace
	if data[0] == 32 {
		data = data[1:]
	}
	// Remove trailing new line
	if data[len(data)-1] == 10 {
		data = data[:len(data)-1]
	}
	return data
}
