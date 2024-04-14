package match

import (
	"github.com/bountysecurity/gbounty/internal/entrypoint"
	"github.com/bountysecurity/gbounty/internal/profile"
	"github.com/bountysecurity/gbounty/internal/request"
)

func reqQueryNameBytes(req *request.Request) []byte {
	return reqQueryBytes(req, profile.ParamURLName)
}

func reqQueryValueBytes(req *request.Request) []byte {
	return reqQueryBytes(req, profile.ParamURLValue)
}

func reqQueryBytes(req *request.Request, ipt profile.InsertionPointType) []byte {
	var b []byte
	for _, e := range entrypoint.NewQueryFinder().Find(*req) {
		if v, ok := e.(entrypoint.Query); ok && v.InsertionPointType() == ipt {
			b = append(b, []byte(v.Value())...)
		}
	}
	return b
}