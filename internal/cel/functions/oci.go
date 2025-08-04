package functions

import (
	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"
	"k8s.io/apiserver/pkg/cel/lazy"
	"oras.land/oras-go/v2/registry"
)

func ToOCI() cel.EnvOption {
	return cel.Function("toOCI",
		cel.MemberOverload("toOCI_any_member",
			[]*cel.Type{cel.AnyType},
			types.NewMapType(types.StringType, types.StringType),
		),
		cel.Overload("toOCI_any",
			[]*cel.Type{cel.AnyType},
			types.NewMapType(types.StringType, types.StringType),
		),
		cel.SingletonUnaryBinding(BindingToOCI),
	)
}

func BindingToOCI(lhs ref.Val) ref.Val {
	s, ok := lhs.Value().(string)
	if !ok {
		return types.NoSuchOverloadErr()
	}
	r, err := registry.ParseReference(s)
	if err != nil {
		return types.WrapErr(err)
	}
	mv := lazy.NewMapValue(types.StringType)
	mv.Append("host", func(value *lazy.MapValue) ref.Val {
		return types.String(r.Host())
	})
	mv.Append("registry", func(value *lazy.MapValue) ref.Val {
		if err := r.ValidateRegistry(); err != nil {
			return types.WrapErr(err)
		}
		return types.String(r.Registry)
	})
	mv.Append("repository", func(value *lazy.MapValue) ref.Val {
		if err := r.ValidateRepository(); err != nil {
			return types.WrapErr(err)
		}
		return types.String(r.Repository)
	})
	mv.Append("reference", func(value *lazy.MapValue) ref.Val {
		if err := r.ValidateReference(); err != nil {
			return types.WrapErr(err)
		}
		return types.String(r.Reference)
	})
	mv.Append("digest", func(value *lazy.MapValue) ref.Val {
		dig, err := r.Digest()
		if err != nil {
			return types.WrapErr(err)
		}
		return types.String(dig.String())
	})
	mv.Append("tag", func(value *lazy.MapValue) ref.Val {
		if err := r.ValidateReferenceAsTag(); err != nil {
			return types.WrapErr(err)
		}
		return types.String(r.Reference)
	})
	return mv
}
