// SPDX-License-Identifier: MIT

package sec

import (
	"errors"
	"testing"
	"time"

	"github.com/otfabric/go-iec60870-5/asdu"
)

// reports returns the statistic reports s received: IOA to the last value.
func reports(t *testing.T, s *station) map[asdu.IOA]asdu.IntegratedTotal {
	t.Helper()
	got := map[asdu.IOA]asdu.IntegratedTotal{}
	for _, d := range s.data {
		a, err := asdu.Decode(d, asdu.IEC104)
		if err != nil || a.Type != asdu.M_IT_TB_1 {
			continue
		}
		if a.Cause != asdu.CauseSpontaneous || a.CommonAddr != 0x1234 {
			t.Errorf("report %v", a)
		}
		for _, o := range a.Objects {
			it := o.(asdu.IntegratedTotal)
			got[it.IOA] = it
		}
	}
	return got
}

// TestStatisticReports: the controlled station reports a statistic in
// M_IT_TB_1, cause spontaneous, at its configured IOA once it grew by its
// threshold (IEC TS 60870-5-7:2025 5.4.2.3, 9.7 and Table 10), as Secure
// Data on the association.
func TestStatisticReports(t *testing.T) {
	p := newPair(t, func(_, b *Config) {
		b.Reports = map[Stat]StatisticReport{
			StatSuccessfulAssociations:        {IOA: 100},
			StatSuccessfulDataAuthentications: {IOA: 101, Threshold: 2},
			StatMessagesReceived:              {IOA: 102}, // default threshold 500
		}
	})
	p.up()
	p.mustReady()
	p.exchange()
	if got := reports(t, p.a); got[100].Value != 1 || len(got) != 1 {
		t.Fatalf("after association: %+v", got)
	}
	p.exchange()
	got := reports(t, p.a)
	if got[101].Value != 2 || got[101].Time.IsZero() {
		t.Fatalf("after two data authentications: %+v", got)
	}
	if _, ok := got[102]; ok {
		t.Error("messages received reported below its threshold of 500")
	}
	// Refused requests are counted and reported through the deadline.
	p2 := newPair(t, func(_, b *Config) {
		b.Roles = []AccessRole{RoleViewer}
		b.Reports = map[Stat]StatisticReport{StatOperationAuthorizationFailures: {IOA: 7}}
	})
	p2.up()
	p2.mustReady()
	ch := p2.b.e.Changed()
	p2.b.e.Refused(asdu.C_SC_NA_1)
	select {
	case <-ch:
	default:
		t.Error("a due report does not wake the link")
	}
	if dl := p2.b.e.Deadline(); dl.After(p2.now) {
		t.Errorf("report due at %v", dl.Sub(p2.now))
	}
	p2.tick()
	if got := reports(t, p2.a); got[7].Value != 1 {
		t.Fatalf("authorization failure report %+v", got)
	}
	if StatDiscardedMessages.DefaultThreshold() != 10 || StatReplyTimeouts.DefaultThreshold() != 3 ||
		StatSuccessfulDataAuthentications.DefaultThreshold() != 100 || StatMessagesSent.DefaultThreshold() != 500 ||
		StatKeyAuthenticationErrors.DefaultThreshold() != 1 {
		t.Error("Table 10 default thresholds")
	}
}

func TestStatisticReportConfig(t *testing.T) {
	a, b := configs()
	a.Reports = map[Stat]StatisticReport{StatMessagesSent: {IOA: 1}}
	if _, err := New(a); !errors.Is(err, ErrConfig) {
		t.Errorf("controlling station with reports: %v", err)
	}
	b.Reports = map[Stat]StatisticReport{NumStats: {IOA: 1}}
	if _, err := New(b); !errors.Is(err, ErrConfig) {
		t.Errorf("unknown statistic: %v", err)
	}
}

// TestStatisticWrap: a statistic is a BCR value, 0 to 2^31-1
// (IEC TS 60870-5-7:2025 5.4.2.3), and wraps to 0 after 2^31-1; the report
// after the wrap sets CY.
func TestStatisticWrap(t *testing.T) {
	p := newPair(t, func(_, b *Config) {
		b.Reports = map[Stat]StatisticReport{StatSuccessfulDataAuthentications: {IOA: 5, Threshold: 1}}
	})
	p.up()
	p.mustReady()
	p.b.e.mu.Lock()
	p.b.e.stats[StatSuccessfulDataAuthentications] = StatMax
	p.b.e.reported[StatSuccessfulDataAuthentications] = StatMax
	p.b.e.mu.Unlock()
	p.exchange()
	if n := p.b.count(StatSuccessfulDataAuthentications); n != 0 {
		t.Fatalf("after 2^31-1: %d", n)
	}
	if got := reports(t, p.a)[5]; got.Value != 0 || !got.Carry {
		t.Fatalf("report after the wrap %+v", got)
	}
}

// TestFileDeleteNeedsFileManagement: deleting a file needs FILEMNGT
// (IEC TS 60870-5-7:2025 8.2, Table 7); selecting or calling one needs
// FILEREAD.
func TestFileDeleteNeedsFileManagement(t *testing.T) {
	del := asdu.New(asdu.CauseFileTransfer, 1, asdu.FileCall{IOA: 1, Name: 2, Qualifier: asdu.FileDelete})
	sel := asdu.New(asdu.CauseFileTransfer, 1, asdu.FileCall{IOA: 1, Name: 2, Qualifier: asdu.FileSelect})
	if RequiredPermissionOf(del) != PermFileManagement || RequiredPermissionOf(sel) != PermFileRead {
		t.Fatalf("delete %v, select %v", RequiredPermissionOf(del), RequiredPermissionOf(sel))
	}
	if RequiredPermissionOf(asdu.New(asdu.CauseActivation, 1, asdu.Interrogation{Qualifier: 20})) != PermMonitorData {
		t.Error("interrogation")
	}
	for _, r := range []AccessRole{RoleViewer, RoleOperator, RoleInstaller, RoleSecAudit} {
		if AuthorizedASDU([]AccessRole{r}, del) || !AuthorizedASDU([]AccessRole{r}, sel) {
			t.Errorf("%s: delete or select", r.Name)
		}
	}
	for _, r := range []AccessRole{RoleEngineer, RoleSecAdmin, RoleRBACMaint} {
		if !AuthorizedASDU([]AccessRole{r}, del) {
			t.Errorf("%s may not delete", r.Name)
		}
	}
	p := newPair(t, func(_, b *Config) { b.Roles = []AccessRole{RoleViewer} })
	if p.b.e.PermitsASDU(del) || !p.b.e.PermitsASDU(sel) || !p.a.e.PermitsASDU(del) {
		t.Error("PermitsASDU")
	}
}

// TestMonitorDataNeedsPermission: a controlled station does not send
// monitor-direction data to a controlling station whose roles lack MONITOR
// DATA, "whether sent spontaneously or sent in response to a request"
// (IEC TS 60870-5-7:2025 8.2); command confirmations and the statistic
// reports still go.
func TestMonitorDataNeedsPermission(t *testing.T) {
	spont, _ := asdu.New(asdu.CauseSpontaneous, 0x1234, asdu.SinglePoint{IOA: 1, Value: true}).Encode(asdu.IEC104)
	for _, c := range []struct {
		role AccessRole
		ok   bool
	}{{RoleSecAdmin, false}, {RoleRBACMaint, false}, {RoleSecAudit, false}, {RoleViewer, true}, {RoleOperator, true}} {
		p := newPair(t, func(_, b *Config) {
			b.Roles = []AccessRole{c.role}
			b.Reports = map[Stat]StatisticReport{StatSuccessfulAssociations: {IOA: 9}}
		})
		p.up()
		p.mustReady()
		out, err := p.b.e.Protect(p.now, spont)
		if c.ok != (err == nil) || (!c.ok && !errors.Is(err, ErrNotPermitted)) {
			t.Errorf("%s: spontaneous data: %v", c.role.Name, err)
		}
		p.send(p.b, out)
		p.run()
		if _, err := p.b.e.Protect(p.now, appData); err != nil {
			t.Errorf("%s: confirmation: %v", c.role.Name, err)
		}
		if reports(t, p.a)[9].Value != 1 {
			t.Errorf("%s: statistic report withheld", c.role.Name)
		}
	}
	if !IsMonitorData(asdu.M_ME_TF_1) || IsMonitorData(asdu.M_EI_NA_1) || IsMonitorData(asdu.C_SC_NA_1) {
		t.Error("IsMonitorData")
	}
}

// TestLinkAlgorithms: over TCP/IP a controlled station accepts the 16-octet
// MACs, over a serial link the 8-octet ones (IEC TS 60870-5-7:2025 9.3.2,
// 9.3.3, 9.5.2, 9.5.3), unless told otherwise.
func TestLinkAlgorithms(t *testing.T) {
	cases := []struct {
		name string
		mod  func(a, b *Config)
		stat Stat
	}{
		{"8-octet MAC over TCP/IP", func(a, _ *Config) { a.MAC = HMACSHA256Serial.Code }, StatKeyAuthAlgorithmFailures},
		{"8-octet data MAC over TCP/IP", func(a, _ *Config) { a.DataProtection = HMACSHA3_256Serial.Code }, StatDataProtectionAlgorithmFailures},
		{"16-octet MAC on a serial link", func(a, b *Config) { a.MAC, b.Serial = HMACSHA256.Code, true }, StatKeyAuthAlgorithmFailures},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newPair(t, c.mod)
			p.up()
			p.until(p.ready, 5*time.Second)
			if p.ready() || p.b.count(c.stat) == 0 {
				t.Errorf("ready %v, %s %d", p.ready(), c.stat, p.b.count(c.stat))
			}
		})
	}
	// Serial on both ends: 8-octet MAC by default, AES-256-GCM accepted.
	p := newPair(t, func(a, b *Config) { a.Serial, b.Serial, a.DataProtection = true, true, AES256GCM })
	p.up()
	p.mustReady()
	if st := p.b.e.Status(); st.MAC != HMACSHA256Serial.Name || st.Data != "AEAD-AES-256-GCM" {
		t.Errorf("serial status %+v", st)
	}
}
