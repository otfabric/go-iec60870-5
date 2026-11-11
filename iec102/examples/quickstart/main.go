// SPDX-License-Identifier: MIT

// Command quickstart runs an IEC 60870-5-102 controlling station and an
// integrated total station (a meter with an hourly load profile) against
// each other over an in-memory line: link initialization, the product
// specification, the station's time and a read of integrated totals by
// time range (C_CI_NU_2). A second part opens and closes a session of the
// Spanish REE profile, which applies only because both sides ask for it.
// It exits when done.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/otfabric/go-iec60870-5/iec102"
	"github.com/otfabric/go-iec60870-5/iec102/client"
	"github.com/otfabric/go-iec60870-5/iec102/ree"
	"github.com/otfabric/go-iec60870-5/iec102/server"
)

func main() {
	// A pipe stands in for the serial line (FT 1.2, two-octet link
	// address); use a serial port or a modem connection in a real system.
	controlling, meter := net.Pipe()

	// The meter: 24 hourly increments of active import (address 1) in
	// record 11 as M_IT_TK_2, each stamped with the end of its hour.
	store := server.NewStore()
	day := time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)
	for h := 1; h <= 24; h++ {
		store.AddPeriod(ree.RecordLoadProfile, iec102.IntegratedTotals{
			T:      iec102.M_IT_TK_2,
			Totals: []iec102.IntegratedTotal{{Addr: ree.AddrActiveImport, Value: uint32(100 + h)}},
			Time:   iec102.TimeAOf(day.Add(time.Duration(h)*time.Hour), nil),
		})
	}
	params := iec102.Params{Profile: ree.Profile} // REE bodies on both sides
	srv, err := server.New(meter, server.Config{
		LinkAddr: 1, CommonAddr: 1, Params: params,
		ProductSpec: iec102.ProductSpec{Manufacturer: 0x42, Product: 1001},
		Store:       store,
		Handler:     &ree.Session{Key: 7},
	})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = srv.Close() }()

	c, err := client.New(controlling, client.Config{LinkAddr: 1, CommonAddr: 1, Params: params})
	if err != nil {
		log.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Init(ctx); err != nil {
		log.Fatal(err)
	}
	if err := ree.Login(ctx, c, 7); err != nil {
		log.Fatal(err)
	}
	fmt.Println("session open")
	spec, err := c.ReadProductSpec(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("manufacturer %d, product %d\n", spec.Manufacturer, spec.Product)
	now, err := c.ReadTime(ctx)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("meter time", now)

	periods, err := c.ReadTotals(ctx, ree.RecordLoadProfile, iec102.Read{
		T: iec102.C_CI_NU_2, First: ree.AddrActiveImport, Last: ree.AddrActiveImport,
		From: iec102.TimeAOf(day.Add(6*time.Hour), nil), To: iec102.TimeAOf(day.Add(9*time.Hour), nil),
	})
	if err != nil {
		log.Fatal(err)
	}
	if len(periods) != 4 {
		log.Fatalf("%d periods, want 4", len(periods))
	}
	for _, p := range periods {
		fmt.Printf("%v  %d kWh\n", p.Time, p.Totals[0].Value)
	}
	if err := ree.Logout(ctx, c); err != nil {
		log.Fatal(err)
	}
	fmt.Println("session closed")
}
