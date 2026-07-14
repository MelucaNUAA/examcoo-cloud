module examcoo-cloud

go 1.22.0

require github.com/gorilla/websocket v1.5.3

replace (
	golang.org/x/sys => github.com/golang/sys v0.28.0
	golang.org/x/text => github.com/golang/text v0.21.0
)
