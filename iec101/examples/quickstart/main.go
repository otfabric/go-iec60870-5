// SPDX-License-Identifier: MIT

// Command quickstart runs an IEC 60870-5-101 controlling and controlled
// station against each other over an in-memory line, in unbalanced mode:
// link initialization, end of initialization, a general interrogation and
// a select-before-operate command. It exits when done.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/iec101"
	"github.com/otfabric/go-iec60870-5/iec101/client"
	"github.com/otfabric/go-iec60870-5/iec101/server"
)

const station asdu.CommonAddr = 1

func main() {
	// A pipe stands in for the serial line; use a go-serial port (or a TCP
	// connection to a terminal server) in a real system.
	controlling, controlled := net.Pipe()
	p := iec101.DefaultParams() // unbalanced, 1-octet link address

	selected := false
	srv, err := server.New(controlled, server.Config{
		Params: p, LinkAddr: 3, CommonAddr: station,
		Handler: server.HandlerFunc(func(s *server.Server, a *asdu.ASDU) {
			switch o := a.First().(type) {
			case asdu.Interrogation:
				_ = s.Confirm(a)
				// One type identification per ASDU.
				for _, obj := range []asdu.InformationObject{
					asdu.SinglePoint{IOA: 100, Value: true},
					asdu.MeasuredFloat{IOA: 200, Value: 49.98},
				} {
					if err := s.Send(asdu.New(o.Qualifier.Cause(), station, obj)); err != nil {
						log.Print(err)
					}
				}
				_ = s.Terminate(a)
			case asdu.SingleCommand:
				switch {
				case o.Select:
					selected = true
					_ = s.Confirm(a)
				case selected:
					selected = false
					_ = s.Confirm(a)
					_ = s.Terminate(a)
				default:
					_ = s.Negative(a)
				}
			default:
				_ = s.Reject(a, asdu.CauseUnknownType)
			}
		}),
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = srv.Close() }()

	c, err := client.New(controlling, client.Config{
		Params: p, LinkAddr: 3,
		Handler: func(a *asdu.ASDU) { fmt.Println("monitor:", a) },
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.WaitUp(ctx); err != nil {
		log.Fatal(err)
	}
	data, err := c.Interrogate(ctx, station, asdu.QOIStation)
	if err != nil {
		log.Fatal(err)
	}
	if len(data) != 2 {
		log.Fatalf("interrogation returned %d ASDU(s), want 2", len(data))
	}
	fmt.Printf("interrogation: %d ASDU(s)\n", len(data))
	if err := c.Command(ctx, station, asdu.SingleCommand{IOA: 500, Value: true, Select: true}); err != nil {
		log.Fatal(err)
	}
	if err := c.Command(ctx, station, asdu.SingleCommand{IOA: 500, Value: true}); err != nil {
		log.Fatal(err)
	}
	fmt.Println("select-before-operate: executed")
}
