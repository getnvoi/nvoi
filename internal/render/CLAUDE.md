# CLAUDE.md — internal/render

Output rendering for both CLIs. Same formatting everywhere.

## Layer rules

| Layer | Writes to stdout? | `fmt.Printf`? | `os.Stdout`? |
|-------|-------------------|---------------|-------------|
| `provider/` | Never | Never | Never |
| `pkg/core/` | Never | Never | Never |
| `infra/` | Never (writes to `io.Writer` param) | Never | Never |
| `kube/` | Never | Never | Never |
| `utils/` | Never | Never | Never |
| `internal/render/` | Yes — it's the renderer | Yes | Yes |
| `internal/core/` | Via `internal/render/` | Via render | Via render |
| `internal/cloud/` | Via `internal/render/` | Via render | Via render |

## Output interface

`pkg/core/` communicates through the `Output` interface on `Cluster`. Seven event types:

```go
type Output interface {
    Command(command, action, name string, extra ...any)  // opens a group
    Progress(msg string)                                  // transient status
    Success(msg string)                                   // step completed
    Warning(msg string)                                   // non-fatal issue
    Info(msg string)                                      // informational
    Error(err error)                                      // terminal failure
    Writer() io.Writer                                    // streaming (build logs, SSH, k3s install)
}
```

`pkg/core/output.go` also defines shared JSONL event types: `Event`, `MarshalEvent`, `ParseEvent`, `ReplayEvent`.

## Renderers

Three implementations:

- **TUI** (`tui.go`) — lipgloss-styled. Default for terminals.
- **JSONL** (`json.go`) — one JSON object per line. `--json` flag.
- **Plain** (`plain.go`) — aligned tags, no ANSI codes. `--ci` flag or auto-detected in non-TTY.

`render.Resolve(jsonFlag, ciFlag)` picks the right renderer.
`render.ReplayLine(jsonlLine, output)` bridges JSONL from the API to any renderer.

## JSONL event format

```jsonl
{"type":"command","command":"instance","action":"set","name":"nvoi-rails-production-master","role":"master"}
{"type":"progress","message":"waiting for SSH on 91.98.91.222"}
{"type":"success","message":"SSH ready"}
{"type":"error","message":"SSH not reachable on 91.98.91.222: timeout"}
```

`type:"command"` opens a group. Everything after belongs to it until the next command or error.

## Error handling

- `pkg/core/` returns errors. Never renders them. Never calls `Output.Error()`.
- Cobra handles all errors. `root.SetErr()` wires cobra's error output through `Output.Error()`.
- No `SilenceErrors`. Cobra is the single error path — style it, not suppress it.
- Single rendering path. No double-printing. No silent swallowing.
- Ctrl+C: `signal.NotifyContext` cancels context → operations abort → exit 1.

## Streaming

`infra/` functions accept `io.Writer` for streaming output. `pkg/core/` passes `Output.Writer()`. In TUI mode, lines are dimmed and indented. In JSONL mode, each line becomes `{"type":"stream","message":"..."}`. The API streams to SSE through the same interface.

## Cluster struct + ProviderRef

Every `pkg/core/` request type embeds `Cluster`:

```go
type Cluster struct {
    AppName, Env, Provider string
    Credentials            map[string]string
    SSHKey                 []byte
    Output                 Output
    SSHFunc                func(ctx, addr) (SSHClient, error)
}
```

`Cluster` provides methods: `Names()`, `Compute()`, `Master()`, `SSH()`, `Log()`.

Secondary providers use `ProviderRef`:

```go
type ProviderRef struct {
    Name  string
    Creds map[string]string
}
```

## infra.Node

```go
type Node struct {
    PublicIP  string
    PrivateIP string
}
```

## ServerStatus

```go
type ServerStatus string
const (
    ServerRunning   ServerStatus = "running"
    ServerOff       ServerStatus = "off"
)
```
