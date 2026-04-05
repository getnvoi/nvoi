package handlers

import (
	"bufio"
	"io"

	"github.com/getnvoi/nvoi/internal/api"
	pkgcore "github.com/getnvoi/nvoi/pkg/core"
	"gorm.io/gorm"
)

// dbOutput implements pkg/core.Output by writing JSONL lines to deployment_step_logs.
type dbOutput struct {
	db     *gorm.DB
	stepID string
}

var _ pkgcore.Output = (*dbOutput)(nil)

func newDBOutput(db *gorm.DB, stepID string) *dbOutput {
	return &dbOutput{db: db, stepID: stepID}
}

func (o *dbOutput) log(ev pkgcore.Event) {
	o.db.Create(&api.DeploymentStepLog{
		DeploymentStepID: o.stepID,
		Line:             pkgcore.MarshalEvent(ev),
	})
}

func (o *dbOutput) Command(command, action, name string, extra ...any) {
	o.log(pkgcore.NewCommandEvent(command, action, name, extra...))
}

func (o *dbOutput) Progress(msg string) {
	o.log(pkgcore.NewMessageEvent(pkgcore.EventProgress, msg))
}

func (o *dbOutput) Success(msg string) {
	o.log(pkgcore.NewMessageEvent(pkgcore.EventSuccess, msg))
}

func (o *dbOutput) Warning(msg string) {
	o.log(pkgcore.NewMessageEvent(pkgcore.EventWarning, msg))
}

func (o *dbOutput) Info(msg string) {
	o.log(pkgcore.NewMessageEvent(pkgcore.EventInfo, msg))
}

func (o *dbOutput) Error(err error) {
	o.log(pkgcore.NewMessageEvent(pkgcore.EventError, err.Error()))
}

func (o *dbOutput) Writer() io.Writer {
	pr, pw := io.Pipe()
	go func() {
		scanner := bufio.NewScanner(pr)
		for scanner.Scan() {
			o.log(pkgcore.NewMessageEvent(pkgcore.EventStream, scanner.Text()))
		}
	}()
	return pw
}
