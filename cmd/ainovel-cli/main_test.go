package main

import "testing"

func TestParseWebAddress(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "default", want: "127.0.0.1:5173"},
		{name: "port", args: []string{"--port", "8080"}, want: "127.0.0.1:8080"},
		{name: "address", args: []string{"--addr", "0.0.0.0:9000"}, want: "0.0.0.0:9000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseWebAddress(tt.args)
			if err != nil {
				t.Fatalf("parseWebAddress() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("parseWebAddress() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseWebAddressRejectsUnknownOption(t *testing.T) {
	if _, err := parseWebAddress([]string{"--unknown"}); err == nil {
		t.Fatal("parseWebAddress() accepted an unknown option")
	}
}
