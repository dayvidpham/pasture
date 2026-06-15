package testutil

import "go.uber.org/goleak"

func GoleakOptions() []goleak.Option {
	return []goleak.Option{
		goleak.IgnoreCurrent(),
		goleak.IgnoreTopFunction("database/sql.(*DB).connectionOpener"),
		goleak.IgnoreAnyFunction("github.com/dbos-inc/dbos-transact-golang/dbos.(*sysDB).recv.func2"),
	}
}
