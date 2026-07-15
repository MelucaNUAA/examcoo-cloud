module examcoo-cloud

go 1.24

require github.com/redis/go-redis/v9 v9.21.0

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
)

replace (
	golang.org/x/sys => github.com/golang/sys v0.28.0
	golang.org/x/text => github.com/golang/text v0.21.0
)
