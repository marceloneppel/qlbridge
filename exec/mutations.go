package exec

import (
	"database/sql/driver"
	"fmt"

	u "github.com/araddon/gou"

	"github.com/araddon/qlbridge/datasource"
	"github.com/araddon/qlbridge/expr"
	"github.com/araddon/qlbridge/vm"
)

var (
	_ = u.EMPTY

	_ TaskRunner = (*Upsert)(nil)
)

// Upsert data task
//
type Upsert struct {
	*TaskBase
	insert     *expr.SqlInsert
	update     *expr.SqlUpdate
	upsert     *expr.SqlUpsert
	db         datasource.Upsert
	dbfeatures *datasource.Features
	dbpatch    datasource.PatchWhere
}

// An insert to write to data source
func NewInsertUpsert(sql *expr.SqlInsert, db datasource.Upsert) *Upsert {
	m := &Upsert{
		TaskBase: NewTaskBase("Upsert"),
		db:       db,
		insert:   sql,
	}
	m.TaskBase.TaskType = m.Type()
	return m
}
func NewUpdateUpsert(sql *expr.SqlUpdate, db datasource.Upsert) *Upsert {
	m := &Upsert{
		TaskBase: NewTaskBase("Upsert"),
		db:       db,
		update:   sql,
	}
	m.TaskBase.TaskType = m.Type()
	return m
}
func NewUpsertUpsert(sql *expr.SqlUpsert, db datasource.Upsert) *Upsert {
	m := &Upsert{
		TaskBase: NewTaskBase("Upsert"),
		db:       db,
		upsert:   sql,
	}
	m.TaskBase.TaskType = m.Type()
	return m
}

func (m *Upsert) setup() {
}

func (m *Upsert) Copy() *Upsert { return &Upsert{} }

func (m *Upsert) Close() error {
	if closer, ok := m.db.(datasource.DataSource); ok {
		if err := closer.Close(); err != nil {
			return err
		}
	}
	if err := m.TaskBase.Close(); err != nil {
		return err
	}
	return nil
}

func (m *Upsert) Run(ctx *Context) error {
	defer ctx.Recover()
	defer close(m.msgOutCh)

	var err error
	var affectedCt int64
	switch {
	case m.insert != nil:
		//u.Debugf("Insert.Run():  %v   %#v", len(m.insert.Rows), m.insert)
		affectedCt, err = m.insertRows(ctx, m.insert.Rows)
	case m.upsert != nil && len(m.upsert.Rows) > 0:
		u.Debugf("Upsert.Run():  %v   %#v", len(m.upsert.Rows), m.upsert)
		affectedCt, err = m.insertRows(ctx, m.upsert.Rows)
	case m.update != nil:
		u.Debugf("Update.Run() %s", m.update.String())
		affectedCt, err = m.updateValues(ctx)
	default:
		u.Warnf("unknown mutation op?  %v", m)
	}
	if err != nil {
		return err
	}
	vals := make([]driver.Value, 2)
	vals[0] = int64(0) // status?
	vals[1] = affectedCt
	m.msgOutCh <- &datasource.SqlDriverMessage{vals, 1}
	return nil
}

func (m *Upsert) updateValues(ctx *Context) (int64, error) {

	select {
	case <-m.SigChan():
		return 0, nil
	default:
		// fall through
	}

	valmap := make(map[string]driver.Value, len(m.update.Values))
	for key, valcol := range m.update.Values {
		//u.Debugf("key:%v  val:%v", key, valcol)
		if valcol.Expr != nil {
			exprVal, ok := vm.Eval(nil, valcol.Expr)
			if !ok {
				u.Errorf("Could not evaluate: %s", valcol.Expr)
				return 0, fmt.Errorf("Could not evaluate expression: %v", valcol.Expr)
			}
			valmap[key] = exprVal.Value()
		} else {
			u.Debugf("%T  %v", valcol.Value.Value(), valcol.Value.Value())
			valmap[key] = valcol.Value.Value()
		}
		//u.Debugf("key:%v col: %v   vals:%v", key, valcol, valmap[key])
	}

	if dbpatch, ok := m.db.(datasource.PatchWhere); ok {
		updated, err := dbpatch.PatchWhere(ctx, m.update.Where, valmap)
		u.Infof("patch: %v %v", updated, err)
		if err != nil {
			return updated, err
		}
		return updated, nil
	} else {
		u.Warnf("does not implement PatchWhere")
	}

	// Need a way to PolyFill and do scan/match?
	// Create a key from Where
	key := datasource.KeyFromWhere(m.update.Where)
	//u.Infof("key: %v", key)
	if _, err := m.db.Put(ctx, key, valmap); err != nil {
		u.Errorf("Could not put values: %v", err)
		return 0, err
	}
	return 1, nil
}

func (m *Upsert) insertRows(ctx *Context, rows [][]*expr.ValueColumn) (int64, error) {
	for i, row := range rows {
		//u.Infof("In Insert Scanner iter %#v", row)
		select {
		case <-m.SigChan():
			if i == 0 {
				return 0, nil
			}
			return int64(i) - 1, nil
		default:
			vals := make([]driver.Value, len(row))
			for x, val := range row {
				if val.Expr != nil {
					exprVal, ok := vm.Eval(nil, val.Expr)
					if !ok {
						u.Errorf("Could not evaluate: %v", val.Expr)
						return 0, fmt.Errorf("Could not evaluate expression: %v", val.Expr)
					}
					//u.Debugf("%T  %v", exprVal.Value(), exprVal.Value())
					vals[x] = exprVal.Value()
				} else {
					//u.Debugf("%T  %v", val.Value.Value(), val.Value.Value())
					vals[x] = val.Value.Value()
				}

				//u.Debugf("%d col: %v   vals:%v", x, val, vals[x])
			}
			if _, err := m.db.Put(ctx, nil, vals); err != nil {
				u.Errorf("Could not put values: %v", err)
				return 0, err
			}
			// continue
		}
	}
	return int64(len(rows)), nil
}

// Delete task
//
type DeletionTask struct {
	*TaskBase
	sql     *expr.SqlDelete
	db      datasource.Deletion
	deleted int
}
type DeletionScanner struct {
	*DeletionTask
}

// An inserter to write to data source
func NewDelete(sql *expr.SqlDelete, db datasource.Deletion) *DeletionTask {
	m := &DeletionTask{
		TaskBase: NewTaskBase("Delete"),
		db:       db,
		sql:      sql,
	}
	m.TaskBase.TaskType = m.Type()
	return m
}

func (m *DeletionTask) Copy() *DeletionTask { return &DeletionTask{} }

func (m *DeletionTask) Close() error {
	if closer, ok := m.db.(datasource.DataSource); ok {
		if err := closer.Close(); err != nil {
			return err
		}
	}
	if err := m.TaskBase.Close(); err != nil {
		return err
	}
	return nil
}

func (m *DeletionTask) Run(context *Context) error {
	defer context.Recover()
	defer close(m.msgOutCh)
	//u.Warnf("DeletionTask.Run():  %v   %#v", len(m.sql.Rows), m.sql)
	u.Infof("In Delete Scanner expr:: %s", m.sql.Where)

	// Hm, how do i evaluate here?  Do i need a special Vm?
	//return fmt.Errorf("Not implemented delete vm")
	deletedCt, err := m.db.DeleteExpression(m.sql.Where)
	if err != nil {
		u.Errorf("Could not put values: %v", err)
		return err
	}
	m.deleted = deletedCt
	vals := make([]driver.Value, 2)
	vals[0] = int64(0)
	vals[1] = int64(deletedCt)
	m.msgOutCh <- &datasource.SqlDriverMessage{vals, 1}

	return nil
}
func (m *DeletionScanner) Run(context *Context) error {
	defer context.Recover()
	defer close(m.msgOutCh)

	//u.Warnf("DeletionTask.Run():  %v   %#v", len(m.sql.Rows), m.sql)

	u.Infof("In Delete Scanner expr %#v", m.sql.Where)
	select {
	case <-m.SigChan():
		return nil
	default:
		if m.sql.Where != nil {
			// Hm, how do i evaluate here?  Do i need a special Vm?
			//return fmt.Errorf("Not implemented delete vm")
			deletedCt, err := m.db.DeleteExpression(m.sql.Where)
			if err != nil {
				u.Errorf("Could not put values: %v", err)
				return err
			}
			m.deleted = deletedCt
			vals := make([]driver.Value, 2)
			vals[0] = int64(0)
			vals[1] = int64(deletedCt)
			m.msgOutCh <- &datasource.SqlDriverMessage{vals, 1}
			//return &qlbResult{affected: deletedCt}
		}
		// continue
	}
	return nil
}
