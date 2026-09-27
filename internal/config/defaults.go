package config

// Neutral technical defaults. Only zero values are filled, so anything set
// in the folder wins. Nothing here describes an application: environments,
// formats, layouts, labels and sidecars always come from the folder.

// DefaultWindowPresets are bound to keys 1…7 when windows.presets is empty.
var DefaultWindowPresets = []string{"15m", "30m", "40m", "45m", "1h", "1d", "2d"}

func applyDefaults(c *Config) {
	h := &c.Huginn
	if len(h.Windows.Presets) == 0 {
		h.Windows.Presets = DefaultWindowPresets
	}
	if h.Windows.TailLines == 0 {
		h.Windows.TailLines = 500
	}
	if h.Windows.HeadLines == 0 {
		h.Windows.HeadLines = 500
	}
	if h.Windows.Default == "" {
		h.Windows.Default = "15m"
	}
	if h.Logs.BufferLines == 0 {
		h.Logs.BufferLines = 50000
	}
	if h.Demo.Seed == 0 {
		h.Demo.Seed = 42
	}
	if h.Demo.Rate == 0 {
		h.Demo.Rate = 1
	}
	h.ReposRoot = ExpandHome(h.ReposRoot)
	if c.UI.Theme == "" {
		c.UI.Theme = "light"
	}
	if c.UI.KeyBar == "" {
		c.UI.KeyBar = "compact"
	}
	for name, l := range c.Layouts {
		if len(l.Zoom.Columns) == 0 {
			l.Zoom, l.ZoomIsStream = l.Stream, true
		}
		for _, line := range []*Line{&l.Stream, &l.Zoom} {
			if line.TimeFormat == "" {
				line.TimeFormat = "15:04:05.000"
			}
			for i := range line.Columns {
				if line.Columns[i].Role == "" {
					line.Columns[i].Role = "plain"
				}
			}
		}
		c.Layouts[name] = l
	}
	for i := range c.Services.Explicit {
		for j := range c.Services.Explicit[i].Workloads {
			w := &c.Services.Explicit[i].Workloads[j]
			if w.Kind == "" {
				w.Kind = "Deployment"
			}
			if e, ok := c.Environments.ByName[w.Env]; w.Namespace == "" && ok && len(e.Namespaces) > 0 {
				w.Namespace = e.Namespaces[0]
			}
		}
	}
}
