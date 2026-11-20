package main

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/compdani/actual-pocket/packages/sync-server-go/internal/config"
)

func TestInjectDefaultCommand(t *testing.T) {
	cfg := &config.Config{Hostname: "::", Port: 5006}

	cases := []struct {
		name string
		args []string
		want []string
	}{
		{
			name: "no arguments serves",
			args: []string{"actual-server"},
			want: []string{"actual-server", "serve", "--http=[::]:5006"},
		},
		{
			name: "flags still serve",
			args: []string{"actual-server", "--dev"},
			want: []string{"actual-server", "serve", "--http=[::]:5006", "--dev"},
		},
		{
			name: "explicit command wins",
			args: []string{"actual-server", "migrate", "up"},
			want: []string{"actual-server", "migrate", "up"},
		},
		{
			name: "explicit serve wins",
			args: []string{"actual-server", "serve", "--http=127.0.0.1:9000"},
			want: []string{"actual-server", "serve", "--http=127.0.0.1:9000"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := injectDefaultCommand(tc.args, cfg)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("injectDefaultCommand(%v) = %v, want %v", tc.args, got, tc.want)
			}
		})
	}
}

func TestInjectDefaultCommandUsesConfiguredAddress(t *testing.T) {
	cfg := &config.Config{Hostname: "0.0.0.0", Port: 8080}

	got := injectDefaultCommand([]string{"actual-server"}, cfg)
	if want := []string{"actual-server", "serve", "--http=0.0.0.0:8080"}; !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestLogStartup(t *testing.T) {
	cfg := &config.Config{
		Env:         "production",
		Mode:        "development",
		DataDir:     "/data",
		ServerFiles: "/data/server-files",
		PBDataDir:   "/data/server-files/pb_data",
		WebRoot:     "/data/web",
	}

	out := &bytes.Buffer{}
	logStartup(out, cfg)

	for _, want := range []string{
		"Data directory: /data\n",
		"Server files: /data/server-files\n",
		"PocketBase data directory: /data/server-files/pb_data\n",
		"Web root: /data/web\n",
		"Running in production mode - Serving static React app\n",
	} {
		if !bytes.Contains(out.Bytes(), []byte(want)) {
			t.Errorf("log output missing %q:\n%s", want, out.String())
		}
	}
}
