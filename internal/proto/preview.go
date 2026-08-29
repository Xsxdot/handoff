package proto

import "time"

// PreviewOpenReq is the owner-side request for a remote preview session.
// Exactly one of Port and Path is set; Via is a session-local allowlist. CWD is
// the CLI working directory used by the owner when creating the session.
type PreviewOpenReq struct {
	Port int      `json:"port,omitempty"`
	Path string   `json:"path,omitempty"`
	Via  []string `json:"via,omitempty"`
	CWD  string   `json:"cwd,omitempty"`
}

// PreviewSession is the persisted owner truth plus the coordinator's machine
// projection. Machine is empty in an owner response and is stamped by the
// coordinator when a remote snapshot is merged.
type PreviewSession struct {
	ID         string    `json:"id"`
	EntryURL   string    `json:"entry_url"`
	Via        []string  `json:"via,omitempty"`
	CWD        string    `json:"cwd"`
	OriginURL  string    `json:"origin_url,omitempty"`
	Branch     string    `json:"branch,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
	TTLSeconds int64     `json:"ttl_seconds"`
	Machine    string    `json:"machine,omitempty"`
}

// PreviewListResp is both the owner list and the coordinator's all-machines
// projection. Machines is present only for scope=all, like ProjectTreeResp.
type PreviewListResp struct {
	Sessions []PreviewSession `json:"sessions"`
	Machines []MachineStatus  `json:"machines,omitempty"`
}

const (
	PreviewEventCreated = "preview.created"
	PreviewEventClosed  = "preview.closed"
)

// PreviewEvent is the live WS mirror message. The close event keeps the full
// session so consumers can remove the exact row without a second lookup.
type PreviewEvent struct {
	Type    string         `json:"type"`
	Session PreviewSession `json:"session"`
	Machine string         `json:"machine,omitempty"`
}

// PreviewOpenResp acknowledges the local desktop open/focus request.
type PreviewOpenResp struct {
	Opened bool `json:"opened"`
}

// PreviewCloseResp is the owner-authoritative close acknowledgement.
type PreviewCloseResp struct {
	OK bool `json:"ok"`
}
