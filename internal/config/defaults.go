package config

// Neutral technical defaults. Only zero values are filled, so anything set
// in the folder wins. Nothing here describes an application: environments,
// formats, layouts, labels and sidecars always come from the folder.

// DefaultWindowPresets are bound to keys 1…7 when windows.presets is empty.
var DefaultWindowPresets = []string{"15m", "30m", "40m", "45m", "1h", "1d", "2d"}

// Default limits of a transform (formats/*.yaml): a value longer than
// DefaultTransformMaxBytes is not read, which bounds a line to about 1 ms;
// DefaultTransformMaxFields bounds the fields one line adds to the buffer.
const (
	// DefaultCopyMaxBytes bounds one copy (ui.yaml copy.max_bytes).
	DefaultCopyMaxBytes = 1 << 20

	DefaultTransformMaxBytes  = 16 << 10
	DefaultTransformMaxFields = 64
)

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
	kafkaDefaults(&h.Kafka)
	if c.UI.Theme == "" {
		c.UI.Theme = "auto"
	}
	if c.UI.KeyBar == "" {
		c.UI.KeyBar = "compact"
	}
	if c.UI.Clipboard == "" {
		c.UI.Clipboard = "auto"
	}
	if c.UI.Copy.MaxBytes == 0 {
		c.UI.Copy.MaxBytes = DefaultCopyMaxBytes
	}
	c.UI.Save.Dir = ExpandHome(c.UI.Save.Dir)
	if c.UI.Mouse == nil {
		on := true
		c.UI.Mouse = &on
	}
	for i := range c.Formats {
		for field, t := range c.Formats[i].Transform {
			if t.MaxBytes == 0 {
				t.MaxBytes = DefaultTransformMaxBytes
			}
			if t.MaxFields == 0 {
				t.MaxFields = DefaultTransformMaxFields
			}
			c.Formats[i].Transform[field] = t
		}
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

func kafkaDefaults(k *Kafka) {
	if k.TailRecords == 0 {
		k.TailRecords = 100
	}
	if k.MaxRecords == 0 {
		k.MaxRecords = 20000
	}
	for _, d := range []struct {
		v   *string
		def string
	}{
		{&k.MaxBufferBytes, "64MiB"},
		{&k.MaxValueBytes, "256KiB"},
		{&k.FetchMaxBytes, "1MiB"},
		{&k.PartitionFetchMaxBytes, "256KiB"},
		{&k.ConnectTimeout, "10s"},
		{&k.RequestTimeout, "30s"},
		{&k.ClientID, "huginn"},
		{&k.Isolation, "read_uncommitted"},
	} {
		if *d.v == "" {
			*d.v = d.def
		}
	}
}
