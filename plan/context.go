package plan

import (
	u "github.com/araddon/gou"
	"golang.org/x/net/context"

	"github.com/araddon/qlbridge/expr"
	"github.com/araddon/qlbridge/rel"
	"github.com/araddon/qlbridge/schema"
)

// Context for Plan/Execution of a Relational task
// - may be transported across network boundaries to particpate in dag of tasks
// - holds references to in-mem data structures for schema
// - holds references to instructions on how to perform task (sql statements, projections)
// - holds task specific state for errors, ids, etc (net.context)
type Context struct {

	// Fields that are transported to participate across network/nodes
	context.Context                  // Cross-boundry net context
	Raw             string           // Raw sql statement
	Stmt            rel.SqlStatement // Original Statement
	Projection      *Projection      // Projection for this context optional

	// Local in-memory helpers not transported across network
	Session expr.ContextReader // Session for this connection
	Schema  *schema.Schema     // this schema for this connection

	// Connection specific errors, handling, also local to this network/node
	DisableRecover bool
	Errors         []error
	errRecover     interface{}
	id             string
	prefix         string
}

// New plan context
func NewContext(query string) *Context {
	return &Context{Raw: query}
}

// called by go routines/tasks to ensure any recovery panics are captured
func (m *Context) Recover() {
	if m == nil {
		return
	}
	if m.DisableRecover {
		return
	}
	if r := recover(); r != nil {
		u.Errorf("context recover: %v", r)
		m.errRecover = r
	}
}

var _ = u.EMPTY
