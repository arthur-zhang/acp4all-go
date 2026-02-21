module acp4all

go 1.25.0

replace github.com/coder/acp-go-sdk => /tmp/acp-go-sdk

require (
	github.com/coder/acp-go-sdk v0.6.3
	github.com/gobwas/glob v0.2.3
	github.com/gorilla/websocket v1.5.3
)
