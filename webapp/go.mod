module github.com/vdibart/polis-cli/webapp

go 1.24.0

require github.com/vdibart/polis-cli/cli-go v0.0.0

require (
	filippo.io/edwards25519 v1.2.0 // indirect
	github.com/yuin/goldmark v1.7.16 // indirect
	golang.org/x/crypto v0.48.0 // indirect
	golang.org/x/sys v0.41.0 // indirect
)

replace github.com/vdibart/polis-cli/cli-go => ../cli-go
