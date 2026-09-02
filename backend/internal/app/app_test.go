package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
)

type fakeDB struct{ closed bool }

func (f *fakeDB) Pool() *pgxpool.Pool        { return nil }
func (f *fakeDB) Ping(context.Context) error { return nil }
func (f *fakeDB) Close()                     { f.closed = true }

type fakeCache struct {
	closed bool
	err    error
}

func (f *fakeCache) Client() *redis.Client      { return nil }
func (f *fakeCache) Ping(context.Context) error { return nil }
func (f *fakeCache) Close() error {
	f.closed = true
	return f.err
}

func TestApp_CloseReleasesEveryDependency(t *testing.T) {
	db := &fakeDB{}
	c := &fakeCache{}
	a := &App{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB:     db,
		Cache:  c,
	}

	a.Close()

	assert.True(t, db.closed)
	assert.True(t, c.closed)
}

func TestApp_CloseToleratesCacheCloseError(t *testing.T) {
	db := &fakeDB{}
	c := &fakeCache{err: errors.New("close failed")}
	a := &App{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		DB:     db,
		Cache:  c,
	}

	assert.NotPanics(t, func() { a.Close() })
	assert.True(t, db.closed, "database must still be closed even if closing the cache errored")
}
