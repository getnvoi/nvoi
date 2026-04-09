package managed

type Context struct {
	DefaultVolumeServer string
}

type Request struct {
	Kind    string
	Name    string
	Env     map[string]string // flat env — credentials come from here
	Context Context
}

type Result struct {
	Bundle Bundle
}

type Bundle struct {
	Kind            string
	RootService     string
	OwnedChildren   []string
	InternalSecrets map[string]string
	ExportedSecrets map[string]string
	Volumes         []Volume
	Storages        []Storage
	Services        []Service
	Crons           []Cron
	Operations      []Operation
}

type Volume struct {
	Name   string
	SizeGB int
	Server string
}

type Storage struct {
	Name       string
	Bucket     string
	CORS       bool
	ExpireDays int
}

type Service struct {
	Name    string
	Image   string
	Port    int
	Command string
	Volumes []string
	Env     []string
	Secrets []string
	Storage []string
}

type Cron struct {
	Name     string
	Schedule string
	Image    string
	Command  string
	Volumes  []string
	Env      []string
	Secrets  []string
	Storage  []string
}

type Operation struct {
	Kind   string
	Name   string
	Params map[string]any
	Owner  Ownership
}

type Ownership struct {
	ManagedKind string
	RootService string
	ChildName   string
}

// BundleShape is the topology of a managed bundle: names only, no values.
// Used for delete operations where credential values are not needed.
type BundleShape struct {
	Kind          string
	RootService   string
	OwnedChildren []string
	Crons         []string
	Services      []string
	Storages      []string
	Volumes       []string
	SecretKeys    []string
}
