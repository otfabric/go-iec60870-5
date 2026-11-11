// SPDX-License-Identifier: MIT

// Command quickstart runs an IEC 60870-5-103 controlling system and an item
// of protection equipment against each other over an in-memory line: link
// initialization with identification, time synchronization, general
// interrogation, a general command and a generic read. It exits when done.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/otfabric/go-iec60870-5/iec103"
	"github.com/otfabric/go-iec60870-5/iec103/client"
	"github.com/otfabric/go-iec60870-5/iec103/server"
)

const fun = iec103.FunDistance

func main() {
	// A pipe stands in for the serial line (FT 1.2, one-octet link
	// address); use a serial port in a real system.
	controlling, protection := net.Pipe()

	srv, err := server.New(protection, server.Config{
		LinkAddr:       1,
		Fun:            fun,
		Identification: iec103.IdentificationOf(iec103.COLWithGeneric, "OTFABRIC", [4]byte{0, 1, 0, 0}),
		Handler: server.HandlerFunc(func(s *server.Server, a *iec103.ASDU) {
			switch b := a.Body.(type) {
			case iec103.GIInit:
				// The GI state of "protection active" (table 9, INF 18).
				_ = s.AnswerGI(a, iec103.New(0, s.CommonAddr(), fun, iec103.InfProtectionActive,
					iec103.TimeTagged{DPI: iec103.DPIOn, Time: s.Now()}))
			case iec103.GeneralCommand:
				_ = s.AckCommand(a, a.Inf == iec103.InfLEDReset)
				if a.Inf == iec103.InfLEDReset {
					_ = s.Event(iec103.CauseRemoteOperation, fun, a.Inf, b.DCO)
				}
			case iec103.GenericCommand:
				if a.Inf == iec103.InfReadHeadings {
					_ = s.GenericAnswer(a, iec103.CauseGenericReadValid,
						iec103.DataSet{GIN: iec103.GIN{Group: 1}, KOD: iec103.KODDescription, Value: iec103.ASCII("Measurands")})
				}
			}
		}),
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = srv.Close() }()

	c, err := client.New(controlling, client.Config{
		LinkAddr: 1,
		NoInitGI: true, // run it explicitly below
		Handler: func(a *iec103.ASDU) {
			if a.Type == iec103.TypeIdentification {
				fmt.Printf("identification: %s, %q\n", a.Cause, a.Body.(iec103.Identification).Manufacturer())
			}
		},
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
	t, err := c.ClockSync(ctx, time.Now())
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("time synchronized, equipment time", t.Format("15:04:05.000"))
	data, err := c.Interrogate(ctx)
	if err != nil {
		log.Fatal(err)
	}
	if len(data) != 1 {
		log.Fatalf("general interrogation returned %d ASDU(s), want 1", len(data))
	}
	fmt.Println("general interrogation:", data[0])
	if err := c.Command(ctx, fun, iec103.InfLEDReset, iec103.DPIOn); err != nil {
		log.Fatal(err)
	}
	fmt.Println("LED reset acknowledged")
	res, err := c.ReadHeadings(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("group %d: %s\n", res.Sets[0].GIN.Group, res.Sets[0].Value.Text())
}
