# Black-box tests of github.com/professor93/rota as a consumer sees it.
#
#   make test     offline: every method, every condition, no network
#   make live     ROTA_LIVE=1: the real accounts in ~/.rota, small prompts
#   make login    ROTA_LOGIN=1: one real claude login into a temporary store
#   make local    offline suite against ../cswapgo instead of the proxy
#   make matrix   docs/matrix.md, every test grouped by the symbol it covers

.PHONY: test live login local matrix

test:
	go test ./...

live:
	ROTA_LIVE=1 go test -count=1 -v ./live/...

login:
	ROTA_LOGIN=1 go run ./cmd/login

local:
	GOWORK=$(CURDIR)/go.work.local go test -count=1 ./...

matrix:
	go run ./cmd/matrix > docs/matrix.md
