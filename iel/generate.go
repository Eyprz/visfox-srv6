// Package iel is the root of the VISFOX-SRv6 Intent-Enabled Layer module.
package iel

//go:generate go tool oapi-codegen -config oapi-codegen.yaml ../api/openapi.yaml
//go:generate go tool generator -path=yang -output_file=internal/domain/tedyang/ted.gen.go -package_name=tedyang -generate_fakeroot -fakeroot_name=device -compress_paths=false -shorten_enum_leaf_names -typedef_enum_with_defmod -generate_simple_unions -exclude_modules=ietf-interfaces yang/visfox-srv6-ted.yang yang/ietf/ietf-network.yang yang/ietf/ietf-network-topology.yang
