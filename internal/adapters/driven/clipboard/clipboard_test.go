package clipboard

import (
	"context"
	"errors"
	"testing"
)

func system(installed []string, env map[string]string) (*System, *[]string) {
	var ran []string
	s := &System{
		LookPath: func(name string) (string, error) {
			for _, n := range installed {
				if n == name {
					return "/usr/bin/" + n, nil
				}
			}
			return "", errors.New("not found")
		},
		Getenv: func(k string) string { return env[k] },
		Run: func(_ context.Context, text, name string, args ...string) error {
			ran = append(ran, name+" "+text)
			return nil
		},
	}
	return s, &ran
}

func TestToolChoice(t *testing.T) {
	for _, tc := range []struct {
		name      string
		installed []string
		env       map[string]string
		want      string
	}{
		{"macOS", []string{"pbcopy"}, nil, "pbcopy"},
		{"Wayland first", []string{"wl-copy", "xclip"}, map[string]string{"WAYLAND_DISPLAY": "w", "DISPLAY": ":0"}, "wl-copy"},
		{"X11", []string{"wl-copy", "xclip"}, map[string]string{"DISPLAY": ":0"}, "xclip"},
		{"xsel", []string{"xsel"}, map[string]string{"DISPLAY": ":0"}, "xsel"},
		{"no display: X tools unusable", []string{"xclip"}, nil, ""},
		{"WSL", []string{"xclip", "clip.exe"}, nil, "clip.exe"},
		{"nothing", nil, nil, ""},
	} {
		s, _ := system(tc.installed, tc.env)
		if got := s.Tool(); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestCopy(t *testing.T) {
	s, ran := system([]string{"pbcopy"}, nil)
	if err := s.Copy(context.Background(), "a line\n"); err != nil || len(*ran) != 1 || (*ran)[0] != "pbcopy a line\n" {
		t.Fatalf("err %v ran %q", err, *ran)
	}
	none, _ := system(nil, nil)
	if err := none.Copy(context.Background(), "x"); !errors.Is(err, ErrNoTool) {
		t.Errorf("no tool: %v", err)
	}
	failing, _ := system([]string{"pbcopy"}, nil)
	failing.Run = func(context.Context, string, string, ...string) error { return errors.New("boom") }
	if err := failing.Copy(context.Background(), "x"); err == nil || err.Error() != "pbcopy: boom" {
		t.Errorf("a failing tool is reported: %v", err)
	}
}

func TestExecuteRealCommand(t *testing.T) {
	if err := execute(context.Background(), "text", "cat"); err != nil {
		t.Skipf("no cat here: %v", err)
	}
	if err := execute(context.Background(), "", "sh", "-c", "echo oops >&2; exit 3"); err == nil || err.Error() != "oops" {
		t.Errorf("stderr explains a failure: %v", err)
	}
}
