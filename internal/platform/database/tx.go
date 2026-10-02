package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jmoiron/sqlx"
)

type txContextKey struct{}

func withTx(ctx context.Context, tx *sqlx.Tx) context.Context {
	return context.WithValue(ctx, txContextKey{}, tx)
}

func txFromContext(ctx context.Context) (*sqlx.Tx, bool) {
	tx, ok := ctx.Value(txContextKey{}).(*sqlx.Tx)
	return tx, ok && tx != nil
}

// TxRunner executes a function inside a single database transaction.
type TxRunner interface {
	RunInTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// TxManager is the PostgreSQL TxRunner.
type TxManager struct {
	db *sqlx.DB
}

func NewTxManager(db *sqlx.DB) *TxManager {
	return &TxManager{db: db}
}

// RunInTx runs fn in a transaction and commits on success. When ctx already
// carries a transaction, fn joins it instead of opening a nested one.
func (m *TxManager) RunInTx(ctx context.Context, fn func(ctx context.Context) error) (err error) {
	if _, ok := txFromContext(ctx); ok {
		return fn(ctx)
	}

	tx, err := m.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = tx.Rollback()
			panic(recovered)
		}
	}()

	if err = fn(withTx(ctx, tx)); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return errors.Join(err, rbErr)
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}
