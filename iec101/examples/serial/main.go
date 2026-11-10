// SPDX-License-Identifier: MIT

// Command serial is an IEC 60870-5-101 controlling station on a byte
// stream: a TCP connection to a serial device server (terminal server)
// that passes the line through unchanged, or a local serial port opened
// by any io.ReadWriteCloser implementation (for example
// github.com/otfabric/go-serial). It initializes the link, runs a general
// interrogation and prints what the station reports until interrupted.
//
//	serial -addr 192.0.2.10:4001 -link 3 -ca 1
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/iec101"
	"github.com/otfabric/go-iec60870-5/iec101/client"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:4001", "terminal server address carrying the serial line")
	linkAddr := flag.Uint("link", 1, "link address of the controlled station")
	ca := flag.Uint("ca", 1, "common address of the controlled station")
	balanced := flag.Bool("balanced", false, "balanced transmission")
	flag.Parse()

	conn, err := net.DialTimeout("tcp", *addr, 5*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	p := iec101.DefaultParams()
	if *balanced {
		p.Mode = iec101.ModeBalanced
	}
	c, err := client.New(conn, client.Config{
		Params: p, LinkAddr: uint16(*linkAddr),
		Handler: func(a *asdu.ASDU) { fmt.Println(a) },
		OnState: func(s iec101.State, err error) { log.Printf("link %s %v", s, err) },
	})
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := c.WaitUp(ctx); err != nil {
		log.Fatal(err)
	}
	if _, err := c.Interrogate(ctx, asdu.CommonAddr(*ca), asdu.QOIStation); err != nil {
		log.Print(err)
	}
	<-ctx.Done()
}
