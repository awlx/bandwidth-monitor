package main

import (
	"strings"
	"testing"
	"time"
)

func TestCollectorInterval(t *testing.T) {
	tests := []struct {
		name    string
		value   string
		want    time.Duration
		wantErr string
	}{
		{name: "default", want: time.Second},
		{name: "slower", value: "5s", want: 5 * time.Second},
		{name: "minimum", value: "100ms", want: 100 * time.Millisecond},
		{name: "too fast", value: "99ms", wantErr: "at least 100ms"},
		{name: "invalid", value: "later", wantErr: "invalid duration"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("COLLECTOR_INTERVAL", test.value)
			got, err := collectorInterval()
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("collectorInterval() error = %v, want containing %q", err, test.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("collectorInterval() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("collectorInterval() = %s, want %s", got, test.want)
			}
		})
	}
}
