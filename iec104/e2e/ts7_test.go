// SPDX-License-Identifier: MIT

package e2e_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
	"github.com/otfabric/go-iec60870-5/iec104/client"
	"github.com/otfabric/go-iec60870-5/iec104/internal/station"
	"github.com/otfabric/go-iec60870-5/iec104/internal/testutil"
	"github.com/otfabric/go-iec60870-5/sec"
)

// IEC TS 60870-5-7:2025 on a secured IEC 104 station: the security
// statistics are reported spontaneously in M_IT_TB_1 at their configured
// IOAs (5.4.2.3, 9.7), and a controlling station whose roles lack MONITOR
// DATA receives no monitor-direction data (8.2): neither through
// Session.Send, Server.Broadcast nor Server.Enqueue.
func TestSecurityTS7(t *testing.T) {
	fx := station.Baseline(t)
	srv, cli, ep := secureOptions(t, fx.Station.CommonAddr, sec.HMACSHA256.Code, func(_ uint16, c *sec.Config) error {
		c.Roles = []sec.AccessRole{sec.RoleSecAdmin}
		c.Reports = map[sec.Stat]sec.StatisticReport{
			sec.StatSuccessfulAssociations:         {IOA: 901},
			sec.StatOperationAuthorizationFailures: {IOA: 900},
		}
		return nil
	})
	l := &link{st: station.Start(t, fx, srv...), fx: fx, ca: fx.Station.CommonAddr, opts: cli, secure: ep}
	rec := &recorder{}
	c := l.dial(t, client.WithHandler(rec))
	ctx := context.Background()
	report := func(ioa asdu.IOA, value int32) {
		t.Helper()
		rec.find(t, "statistic report", func(a *asdu.ASDU) bool {
			if a.Type != asdu.M_IT_TB_1 || a.Cause != asdu.CauseSpontaneous {
				return false
			}
			it := a.First().(asdu.IntegratedTotal)
			return it.IOA == ioa && it.Value == value
		})
	}
	report(901, 1)
	negative(t, c.Command(ctx, l.ca, asdu.SingleCommand{IOA: 500, Value: true}), asdu.CauseNotAuthorized)
	report(900, 1)

	ses := l.st.Server.Sessions()
	if len(ses) != 1 {
		t.Fatalf("%d sessions", len(ses))
	}
	spont := asdu.New(asdu.CauseSpontaneous, l.ca, asdu.SinglePoint{IOA: 100, Value: true})
	if err := ses[0].Send(ctx, spont); !errors.Is(err, sec.ErrNotPermitted) {
		t.Errorf("Session.Send: %v", err)
	}
	if n, err := l.st.Server.Broadcast(ctx, spont); n != 0 || err != nil {
		t.Errorf("Broadcast: %d, %v", n, err)
	}
	if err := l.st.Server.Enqueue(spont); err != nil {
		t.Fatal(err)
	}
	testutil.Eventually(t, "the event dropped", func() bool { return l.st.Server.Pending(ses[0].Group()) == 0 })
	time.Sleep(50 * time.Millisecond)
	for _, a := range rec.all() {
		if a.Type == asdu.M_SP_NA_1 {
			t.Errorf("monitor data reached a SECADM: %v", a)
		}
	}
}

// File deletion needs FILEMNGT (IEC TS 60870-5-7:2025 8.2): a VIEWER, which
// may select and read files, is refused the delete qualifier of F_SC_NA_1.
func TestSecurityFileDelete(t *testing.T) {
	fx := station.Baseline(t)
	srv, cli, ep := secureOptions(t, fx.Station.CommonAddr, sec.HMACSHA256.Code, func(_ uint16, c *sec.Config) error {
		c.Roles = []sec.AccessRole{sec.RoleViewer}
		return nil
	})
	l := &link{st: station.Start(t, fx, srv...), fx: fx, ca: fx.Station.CommonAddr, opts: cli, secure: ep}
	rec := &recorder{}
	c := l.dial(t, client.WithHandler(rec))
	f := fx.Files[0]
	if err := c.Send(context.Background(), asdu.New(asdu.CauseFileTransfer, l.ca,
		asdu.FileCall{IOA: f.IOA, Name: f.Name, Qualifier: asdu.FileDelete})); err != nil {
		t.Fatal(err)
	}
	rec.find(t, "the refusal", func(a *asdu.ASDU) bool {
		return a.Type == asdu.F_SC_NA_1 && a.Cause == asdu.CauseNotAuthorized && a.Negative
	})
}
