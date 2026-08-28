package testutil

import "go.uber.org/goleak"

func GoleakOptions() []goleak.Option {
	return []goleak.Option{
		goleak.IgnoreCurrent(),
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
	}
}
