package logging_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"

	"github.com/AriasYarangaIam/CheckPOS/internal/platform/logging"
)

func TestNewHonoursTheConfiguredLevel(t *testing.T) {
	cases := []struct {
		level        string
		debugVisible bool
	}{
		{level: "debug", debugVisible: true},
		{level: "info", debugVisible: false},
		{level: "warn", debugVisible: false},
		{level: "error", debugVisible: false},
	}

	for _, tc := range cases {
		t.Run(tc.level, func(t *testing.T) {
			var buf bytes.Buffer
			logger := logging.New(tc.level, &buf)

			logger.Debug("a debug line")

			if got := strings.Contains(buf.String(), "a debug line"); got != tc.debugVisible {
				t.Errorf("LOG_LEVEL=%s: línea de debug visible = %t, want %t (log: %q)", tc.level, got, tc.debugVisible, buf.String())
			}
		})
	}
}

// TestNewFallsBackToInfoForAnUnknownLevel fija el comportamiento documentado: un nivel
// desconocido se comporta como info. Llegar aquí con un valor inválido es un defecto de
// programación —config.Load ya lo validó—, y un logger que no arranca es peor.
func TestNewFallsBackToInfoForAnUnknownLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New("not-a-level", &buf)

	logger.Debug("a debug line")
	if strings.Contains(buf.String(), "a debug line") {
		t.Errorf("un nivel desconocido debe comportarse como info (debug oculto); obtuvo %q", buf.String())
	}

	logger.Info("an info line")
	if !strings.Contains(buf.String(), "an info line") {
		t.Errorf("un nivel desconocido debe comportarse como info (info visible); obtuvo %q", buf.String())
	}
}

func TestNewProducesOneJSONRecordPerLine(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.New("info", &buf)

	logger.Info("request handled", "status", 200, "request_id", "abc123")

	var record map[string]any
	if err := json.Unmarshal(buf.Bytes(), &record); err != nil {
		t.Fatalf("la línea de log no es JSON: %v (%q)", err, buf.String())
	}
	if record["msg"] != "request handled" {
		t.Errorf("msg = %v, want %q", record["msg"], "request handled")
	}
	if record["status"] != float64(200) {
		t.Errorf("status = %v, want 200", record["status"])
	}
	if record["request_id"] != "abc123" {
		t.Errorf("request_id = %v, want %q", record["request_id"], "abc123")
	}
	if record["level"] != "INFO" {
		t.Errorf("level = %v, want %q", record["level"], "INFO")
	}
}

func TestNewRequestIDIsFreshRandomHex(t *testing.T) {
	first, second := logging.NewRequestID(), logging.NewRequestID()

	if first == second {
		t.Errorf("dos llamadas devolvieron el mismo id %q; los identificadores no deben repetirse", first)
	}
	if len(first) != 32 {
		t.Errorf("len(id) = %d, want 32 caracteres hexadecimales (16 bytes aleatorios); id = %q", len(first), first)
	}
	if _, err := hex.DecodeString(first); err != nil {
		t.Errorf("el id %q no es hexadecimal: %v", first, err)
	}
}

func TestRequestIDTravelsInTheContext(t *testing.T) {
	ctx := logging.WithRequestID(context.Background(), "req-42")

	if got := logging.RequestID(ctx); got != "req-42" {
		t.Errorf("RequestID(ctx) = %q, want %q", got, "req-42")
	}
	if got := logging.RequestID(context.Background()); got != "" {
		t.Errorf("RequestID sobre un contexto vacío = %q, want la cadena vacía", got)
	}
}
