package app

import (
	"strings"
	"testing"
	"time"

	"wool/internal/alerts"
	"wool/internal/sysinfo"
)

func TestEvaluateAlertsUpdatesStatusListAndCooldown(t *testing.T) {
	m := testModel(t)
	m.alertEngine = alerts.New(alerts.Config{
		Enabled:     true,
		TempC:       60,
		VRAMPercent: 95,
		Cooldown:    time.Minute,
	})
	at := time.Date(2026, 11, 12, 12, 0, 0, 0, time.UTC)
	hot := sysinfo.Snapshot{GPUs: []sysinfo.GPU{{HasTemp: true, TempC: 70}}}

	m.applyTick(tickMsg{at: at, snap: hot})
	if len(m.alertList) != 1 || m.alertList[0].Rule != alerts.RuleTemperature {
		t.Fatalf("alerts = %+v", m.alertList)
	}

	// The same condition inside the cooldown is not appended again.
	m.applyTick(tickMsg{at: at.Add(30 * time.Second), snap: hot})
	if len(m.alertList) != 1 {
		t.Fatalf("alerts after cooldown tick = %+v", m.alertList)
	}

	m.width, m.height = 120, 40
	out := m.View()
	if !strings.Contains(out, "ALERTS") || !strings.Contains(out, "temperature") {
		t.Fatalf("status panel does not show the alert:\n%s", out)
	}
}

func TestEvaluateAlertsDoesNothingWhenDisabled(t *testing.T) {
	m := testModel(t)
	m.alertEngine = alerts.New(alerts.Config{})
	m.applyTick(tickMsg{
		at:   time.Now(),
		snap: sysinfo.Snapshot{GPUs: []sysinfo.GPU{{HasTemp: true, TempC: 120}}},
	})
	if len(m.alertList) != 0 {
		t.Fatalf("disabled engine produced %+v", m.alertList)
	}
}
