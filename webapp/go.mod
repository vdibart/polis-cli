module github.com/vdibart/polis-cli/webapp

go 1.24.4

require github.com/vdibart/polis-cli/cli-go v0.0.0

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/aymerick/douceur v0.2.0 // indirect
	github.com/gorilla/css v1.0.1 // indirect
	github.com/microcosm-cc/bluemonday v1.0.27 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/net v0.49.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
)

require (
	github.com/tyler-smith/go-bip39 v1.1.0 // indirect
	github.com/yuin/goldmark v1.7.16 // indirect
	golang.org/x/term v0.40.0 // indirect
)

replace github.com/vdibart/polis-cli/cli-go => ../cli-go
