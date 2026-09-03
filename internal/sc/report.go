package sc

type Report struct {
	ModelVersion     string       `json:"model_version"`
	Structure        int          `json:"structure"`
	State            int          `json:"state"`
	StructuralCycles int          `json:"structural_cycles"`
	StateCycles      int          `json:"state_cycles"`
	Total            int          `json:"total"`
	Roots            []RootReport `json:"roots"`
	Files            []FileReport `json:"files"`
	Hotspots         []Hotspot    `json:"hotspots"`
}

type RootReport struct {
	ID        string `json:"id"`
	Identity  string `json:"identity"`
	Kind      string `json:"kind"`
	File      string `json:"file"`
	Structure int    `json:"structure"`
	State     int    `json:"state"`
	Total     int    `json:"total"`
}

type FileReport struct {
	Path      string `json:"path"`
	Structure int    `json:"structure"`
	State     int    `json:"state"`
	Total     int    `json:"total"`
}

type Hotspot struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	Path         string `json:"path"`
	StartLine    int    `json:"start_line"`
	StartColumn  int    `json:"start_column"`
	Contribution int    `json:"contribution"`
	RootID       string `json:"root_id,omitempty"`
}
