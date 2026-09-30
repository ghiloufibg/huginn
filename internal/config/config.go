// Package config loads and validates Huginn's config folder.
//
// The folder is the only way application knowledge enters Huginn:
// environments, repository mapping, sidecars, log formats and line layouts
// (docs/CONFIG.md, docs/DECISIONS.md D-030). Configuration is plain data:
// adapters receive only their own part from the composition root, and the
// core never reads it.
package config

import (
	"fmt"

	"go.yaml.in/yaml/v3"
)

// Version is the only folder structure version this build reads.
const Version = 1

// Config is a loaded config folder.
type Config struct {
	// Dir is the folder it was read from (for messages).
	Dir          string
	Huginn       Huginn
	Environments Environments
	Services     Services
	Containers   Containers
	UI           UI
	// Formats are in file name order: the first matching one wins.
	Formats []Format
	// Layouts by name (file name without extension).
	Layouts map[string]Layout

	pos map[string]positions // key positions by file
}

// Problem returns a problem located at path in file (or at its closest
// parent key), for checks done after loading, such as compiling layout
// templates or the keymap.
func (c *Config) Problem(file, path, format string, args ...any) Problem {
	line, col := c.pos[file].at(path)
	return Problem{File: file, Line: line, Col: col, Path: path, Msg: fmt.Sprintf(format, args...)}
}

// Huginn is huginn.yaml: general settings.
type Huginn struct {
	Version    int     `yaml:"version" doc:"Structure version of this file; must be 1." required:"true"`
	DefaultEnv string  `yaml:"default_env" doc:"Environment opened when none is given on the command line; a key of environments.yaml." required:"true"`
	ReposRoot  string  `yaml:"repos_root" doc:"Folder containing your repositories, used by the manifests rule of services.yaml. ~ is expanded."`
	Windows    Windows `yaml:"windows" doc:"Time-window presets of the logs screen."`
	Logs       Logs    `yaml:"logs" doc:"Log loading limits."`
	Demo       Demo    `yaml:"demo" doc:"Synthetic cluster used by --demo."`
}

// Windows configures the time-window presets.
type Windows struct {
	Presets   []string `yaml:"presets" doc:"Windows bound to keys 1…7, e.g. [15m, 30m, 1h, 1d]; at most 7. Default: 15m 30m 40m 45m 1h 1d 2d."`
	TailLines int      `yaml:"tail_lines" doc:"Lines loaded by the tail window (key 0). Default 500."`
	HeadLines int      `yaml:"head_lines" doc:"Lines loaded per container by the head window (key 9): the first lines the node keeps. At most logs.buffer_lines. Default 500."`
	Default   string   `yaml:"default" doc:"Window used when a logs screen opens: a duration such as 15m, tail or head. Default 15m."`
}

// Logs configures log loading.
type Logs struct {
	BufferLines int `yaml:"buffer_lines" doc:"Maximum lines kept in memory per logs screen; older lines are dropped. At least 1000. Default 50000."`
}

// Demo configures the synthetic cluster.
type Demo struct {
	Seed int64   `yaml:"seed" doc:"Random seed; the same seed gives the same cluster and logs. Default 42."`
	Rate float64 `yaml:"rate" doc:"Average live log lines per second per pod. Default 1."`
}

// EnvironmentsFile is environments.yaml.
type EnvironmentsFile struct {
	Version      int                    `yaml:"version" doc:"Structure version of this file; must be 1." required:"true"`
	Environments map[string]Environment `yaml:"environments" doc:"Environments by name (lower-case letters, digits, '-'), in the order the environment picker shows them." required:"true"`
}

// Environments are the configured environments in file order.
type Environments struct {
	Names  []string
	ByName map[string]Environment
}

// Environment describes where one environment lives.
type Environment struct {
	Context       string   `yaml:"context" doc:"kubeconfig context name; empty means the current context."`
	Namespaces    []string `yaml:"namespaces" doc:"Namespaces holding the environment's workloads. Set this or namespace_from."`
	NamespaceFrom string   `yaml:"namespace_from" doc:"Read the namespace from a sops-encrypted dotenv file instead: sops:<file>#<key>."`
	Production    bool     `yaml:"production" doc:"Show the production banner."`
}

// Services is services.yaml: how workloads map to repositories.
type Services struct {
	Version   int           `yaml:"version" doc:"Structure version of this file; must be 1." required:"true"`
	Resolve   []string      `yaml:"resolve" doc:"Rules tried in order for each workload; the first that names a repository wins." enum:"explicit,labels,manifests" required:"true"`
	LabelKeys []string      `yaml:"label_keys" doc:"Workload labels or annotations whose value is the repository name (rule labels)."`
	Manifests Manifests     `yaml:"manifests" doc:"Where to find an environment's Kubernetes manifests in a repository (rule manifests)."`
	Explicit  []RepoMapping `yaml:"explicit" doc:"Repositories and their workloads, listed by hand (rule explicit)."`
	// StandalonePods is nil when not set: shown by default.
	StandalonePods *bool `yaml:"standalone_pods" doc:"List the pods no known workload owns (a bare pod, a Job made by hand, pods of an unknown controller) as their own rows, grouped by owner. Default true."`
}

// ShowStandalone reports whether standalone pods are listed (default yes).
func (s Services) ShowStandalone() bool { return s.StandalonePods == nil || *s.StandalonePods }

// Manifests configures manifest scanning.
type Manifests struct {
	OverlayGlob string   `yaml:"overlay_glob" doc:"Glob, relative to a repository, of an environment's overlay folder; {env} is replaced by the environment name."`
	Files       []string `yaml:"files" doc:"Entry files read in an overlay folder, e.g. [kustomization.yaml]."`
}

// RepoMapping maps a repository to its workloads explicitly.
type RepoMapping struct {
	Repo      string        `yaml:"repo" doc:"Repository name as shown on the services screen." required:"true"`
	Workloads []WorkloadRef `yaml:"workloads" doc:"Workloads owned by the repository." required:"true"`
}

// WorkloadRef identifies one workload.
type WorkloadRef struct {
	Env       string `yaml:"env" doc:"Environment name; a key of environments.yaml." required:"true"`
	Namespace string `yaml:"namespace" doc:"Namespace; default: the environment's first namespace."`
	Kind      string `yaml:"kind" doc:"Workload kind. Default Deployment." enum:"Deployment,StatefulSet,DaemonSet,CronJob"`
	Name      string `yaml:"name" doc:"Workload name." required:"true"`
}

// Containers is containers.yaml: which containers are sidecars.
type Containers struct {
	Version     int      `yaml:"version" doc:"Structure version of this file; must be 1." required:"true"`
	Hide        []string `yaml:"hide" doc:"Sidecars: containers not followed in app mode, nor counted in restarts, readiness and version. A name matches that container and name-*; an image name (last path element without tag, e.g. proxyv2) matches containers running it."`
	AlwaysShow  []string `yaml:"always_show" doc:"Container names always shown, even if matched by hide."`
	ShowInit    bool     `yaml:"show_init" doc:"Treat init containers as application containers."`
	DefaultMode string   `yaml:"default_mode" doc:"Containers the logs screen opens on: app (application containers, the ones not matched by hide) or all (sidecars and init containers too). Key A switches during a session. Default app." enum:"app,all"`
}

// UI is ui.yaml: personal display choices.
type UI struct {
	Version         int                 `yaml:"version" doc:"Structure version of this file; must be 1." required:"true"`
	Theme           string              `yaml:"theme" doc:"Color theme. Default light." enum:"light,accessible,classic,none"`
	PaintBackground bool                `yaml:"paint_background" doc:"Paint the theme background instead of using the terminal's."`
	KeyBar          string              `yaml:"key_bar" doc:"Key bar at the bottom. Default compact." enum:"compact,full,hidden"`
	Keymap          map[string][]string `yaml:"keymap" doc:"Action name to keys, replacing the default keys of that action, e.g. {follow: [f, ctrl+l]}."`
	LogColumns      []string            `yaml:"log_columns" doc:"Columns shown when a logs screen opens: pod and names of layout columns. Empty: every visible column, narrowed automatically."`
}

// Format is one file of formats/: how to read a log line.
type Format struct {
	// Name is the file name without extension; File the path in the folder.
	Name, File string               `yaml:"-"`
	Version    int                  `yaml:"version" doc:"Structure version of this file; must be 1." required:"true"`
	Decoder    string               `yaml:"decoder" doc:"json: one JSON object per line; regex: text lines read with pattern; plain: no structure." enum:"json,regex,plain" required:"true"`
	Match      Match                `yaml:"match" doc:"Containers read with this format. Formats are tried in file name order; the first match wins. No match section: every container."`
	Fields     FieldMap             `yaml:"fields" doc:"json decoder: where each standard field is, as candidate JSON paths (first present wins; dots walk into objects)."`
	Levels     map[string]Paths     `yaml:"levels" doc:"Extra spellings of each level in these logs (case ignored), e.g. {error: [\"50\", FATAL]}. Common spellings are known already." keys:"error,warn,info,debug"`
	Hidden     []string             `yaml:"hidden" doc:"json decoder: fields (globs on dotted paths) never shown on the stream nor searched, only in the hidden fields of zoom, e.g. [\"kubernetes.*\"]."`
	Transform  map[string]Transform `yaml:"transform" doc:"json decoder: keep part of a standard field's value and extract fields from it, by field, e.g. {message: {pattern: '- (?P<message>.*?) -'}}." keys:"message,logger,thread,trace_id,app,pid"`
	Pattern    string               `yaml:"pattern" doc:"regex decoder: Go regular expression with named groups; time, level, logger, thread, message, trace_id, app and pid are standard fields, other groups become extra fields. message is required."`
	TimeFormat string               `yaml:"time_format" doc:"regex decoder: Go reference layout of the time group, e.g. 02/Jan/2006:15:04:05 -0700. Default RFC 3339."`
	LevelFrom  LevelFrom            `yaml:"level_from" doc:"json and regex decoders: raise the level from another field, e.g. the HTTP status. It never lowers the level."`
	Layout     string               `yaml:"layout" doc:"Layout used to draw these lines: a file name of layouts/ without extension." required:"true"`
}

// Match selects the containers of a format.
type Match struct {
	Repos      []string `yaml:"repos" doc:"Globs on the repository name, e.g. [\"payment-*\"]. Empty: any repository."`
	Containers []string `yaml:"containers" doc:"Globs on the container name, e.g. [nginx]. Empty: any container."`
}

// FieldMap maps standard fields to candidate JSON paths.
type FieldMap struct {
	Time    Paths `yaml:"time" doc:"Entry time (RFC 3339 string or epoch seconds/milliseconds)."`
	Level   Paths `yaml:"level" doc:"Severity."`
	Logger  Paths `yaml:"logger" doc:"Logger or class name."`
	Thread  Paths `yaml:"thread" doc:"Thread name."`
	Message Paths `yaml:"message" doc:"Message text; required by the json decoder."`
	Stack   Paths `yaml:"stack" doc:"Stack trace."`
	TraceID Paths `yaml:"trace_id" doc:"Correlation or trace identifier."`
	App     Paths `yaml:"app" doc:"Application name."`
	PID     Paths `yaml:"pid" doc:"Process id."`
}

// Transform reads the value of a standard field of a json format with a
// regular expression: it keeps part of the value and extracts fields.
type Transform struct {
	Pattern string   `yaml:"pattern" doc:"Go regular expression with a group named after the field, e.g. (?P<message>…): when it matches, the group becomes the field's value; otherwise nothing changes. Other named groups become fields of the line (standard ones fill their field when the JSON left it empty); time and stack groups are not allowed." required:"true"`
	Pairs   []string `yaml:"pairs" doc:"Groups of pattern holding key=value text, e.g. [before, after]: each pair becomes a field; empty values are left out."`
	// PairPattern reads the pairs groups; empty means key=value separated by
	// white space.
	PairPattern string `yaml:"pair_pattern" doc:"Go regular expression reading one pair of a pairs group, with the groups key and value, e.g. '(?P<key>[\\w.-]+): (?P<value>[^;]*);?'. The matches must cover the group except white space, otherwise the group is kept whole. Default: key=value separated by white space."`
	// MaxBytes and MaxFields bound the cost of one line; zero means the
	// default.
	MaxBytes  int `yaml:"max_bytes" doc:"Values longer than this many bytes are left as they are (the regular expression costs about 50 µs per KiB). At least 1. Default 16384."`
	MaxFields int `yaml:"max_fields" doc:"At most this many fields extracted per line; the rest stays in the raw view. At least 1. Default 64."`
}

// byName returns the paths of a standard field by its YAML name, and
// whether the name is one of the fields.
func (m FieldMap) byName(name string) (Paths, bool) {
	switch name {
	case "time":
		return m.Time, true
	case "level":
		return m.Level, true
	case "logger":
		return m.Logger, true
	case "thread":
		return m.Thread, true
	case "message":
		return m.Message, true
	case "stack":
		return m.Stack, true
	case "trace_id":
		return m.TraceID, true
	case "app":
		return m.App, true
	case "pid":
		return m.PID, true
	}
	return nil, false
}

// LevelFrom raises the level of a line from the value of another field.
type LevelFrom struct {
	Field string            `yaml:"field" doc:"Field whose value decides the level, e.g. status: a group of pattern (regex), or a JSON path or a field extracted by a transform (json)."`
	Map   map[string]string `yaml:"map" doc:"Glob on the value to level (error, warn, info, debug), tried longest glob first, e.g. {\"5*\": error, \"4*\": warn}. The line takes the more severe of its level and the matched one; no match keeps its level."`
}

// Layout is one file of layouts/: how to draw a log line.
type Layout struct {
	Name, File string `yaml:"-"`
	Version    int    `yaml:"version" doc:"Structure version of this file; must be 1." required:"true"`
	Stream     Line   `yaml:"stream" doc:"The line on the logs screen. Its columns are the ones c hides one by one and C toggles." required:"true"`
	// ZoomIsStream is set when the file has no zoom line: Zoom is then a
	// copy of Stream.
	ZoomIsStream bool  `yaml:"-"`
	Zoom         Line  `yaml:"zoom" doc:"The first line of the zoom view (enter). Default: the stream line."`
	Stack        Stack `yaml:"stack" doc:"Stack traces."`
}

// Line is a sequence of columns followed by the message.
type Line struct {
	TimeFormat string    `yaml:"time_format" doc:"Go reference layout of {time}. Default 15:04:05.000."`
	Columns    []Column  `yaml:"columns" doc:"Columns drawn before the message, in this order, separated by one space." required:"true"`
	Separator  Separator `yaml:"separator" doc:"Text between the columns and the message."`
}

// Column is one column of a line.
type Column struct {
	Name      string `yaml:"name" doc:"Column name, unique in the line; shown in the columns picker and the status bar." required:"true"`
	Key       string `yaml:"key" doc:"stream only: one letter toggling the column in the columns picker (C). p, z, r and f are taken."`
	Show      string `yaml:"show" doc:"Template of the column, e.g. \"[{thread|last:15|right:15}]\". A column whose text is empty is left out." required:"true"`
	Role      string `yaml:"role" doc:"Color role from the theme. time columns follow ctrl+t; level columns hidden make WARN messages colored. Default plain." enum:"time,level,thread,logger,pid,dim,plain"`
	HideBelow int    `yaml:"hide_below" doc:"stream only: hidden automatically when the terminal is narrower than this many cells, until you choose columns."`
	Visible   *bool  `yaml:"visible" doc:"stream only: shown when a logs screen opens. Default true."`
}

// Separator is the text between the columns and the message.
type Separator struct {
	Text  string   `yaml:"text" doc:"Separator text, e.g. \": \"."`
	After []string `yaml:"after" doc:"Shown only when one of these columns is shown. Empty: whenever any column is shown."`
}

// Stack configures stack traces.
type Stack struct {
	FrameworkPrefixes []string `yaml:"framework_prefixes" doc:"Frames starting with one of these prefixes are framework frames; the others are highlighted as your code in zoom, e.g. [java., org.springframework.]."`
}

// Paths is a list of candidate JSON paths. In YAML it may be written as a
// single string or a list.
type Paths []string

// UnmarshalYAML accepts a scalar or a sequence.
func (p *Paths) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind == yaml.ScalarNode {
		*p = Paths{n.Value}
		return nil
	}
	var xs []string
	if err := n.Decode(&xs); err != nil {
		return err
	}
	*p = xs
	return nil
}
