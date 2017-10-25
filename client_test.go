/* This Source Code Form is subject to the terms of the Mozilla Public
 * License, v. 2.0. If a copy of the MPL was not distributed with this
 * file, You can obtain one at http://mozilla.org/MPL/2.0/. */

package sse

import (
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	_ "net/http/pprof"

	. "github.com/smartystreets/goconvey/convey"
)

var url string

func setup() {
	// New Server
	s := New()

	mux := http.NewServeMux()
	mux.HandleFunc("/events", s.HTTPHandler)
	server := httptest.NewServer(mux)
	url = server.URL + "/events"

	s.CreateStream("test")

	// Send continuous string of events to the client
	go func(s *Server) {
		for {
			s.Publish("test", &Event{Data: []byte("ping")})
			time.Sleep(time.Millisecond * 500)
		}
	}(s)
}

func TestClient(t *testing.T) {
	go func() {
		log.Println(http.ListenAndServe("localhost:6060", nil))
	}()
	setup()
	Convey("Given a new Client", t, func() {
		c := NewClient(url)

		Convey("When connecting to a new stream", func() {
			Convey("It should receive events ", func() {
				ctx, cancel := context.WithCancel(context.Background())
				events := make(chan *Event)
				var cErr error
				done := make(chan bool)
				go func(cErr error) {
					cErr = c.SubscribeContext(ctx, "test", func(msg *Event) {
						if msg.Data != nil {
							events <- msg
							return
						}
					})
					done <- true
				}(cErr)
				ctxWait, cancelWait := context.WithTimeout(context.Background(), 15*time.Second)
				err := c.WaitForConnect(ctxWait)
				cancelWait()
				So(err, ShouldBeNil)

				for i := 0; i < 5; i++ {
					msg, err := wait(events, time.Second*1)
					So(err, ShouldBeNil)
					So(string(msg), ShouldEqual, "ping")
				}
				cancel()
				select {
				case <-done:
					// all ok
				case <-time.After(180 * time.Second):
					So("timeout", ShouldEqual, "")
				}

				So(cErr, ShouldBeNil)
			})
		})
	})
}
