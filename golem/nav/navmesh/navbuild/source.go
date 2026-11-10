// Package navbuild validates explicit server navigation source exports. It does
// not infer walkable surfaces from collision footprints, entity schemas, or a
// client's navigation mesh. Geometry generation stays in navmesh-go.
package navbuild

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"

	"github.com/demiurgos-hub/navmesh-go/artifact"
	"github.com/demiurgos-hub/navmesh-go/bake"
	"github.com/demiurgos-hub/navmesh-go/geom"
)

// Source is the same resolved source/configuration document accepted by
// navmesh-bake. Explicit mesh instances retain stable identities for rebuilding.
type Source struct {
	Generator string `json:",omitempty"`
	Scene     geom.Scene
	Config    bake.Config
}

// Decode reads at most 256 MiB and rejects unknown fields or trailing values.
// Geometry/settings validation happens before callers schedule expensive work.
func Decode(reader io.Reader) (Source, error) {
	var source Source
	if reader == nil {
		return source, errors.New("navbuild: nil source reader")
	}
	data, err := io.ReadAll(io.LimitReader(reader, (256<<20)+1))
	if err != nil {
		return source, err
	}
	if len(data) > 256<<20 {
		return source, errors.New("navbuild: source exceeds 256 MiB")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&source); err != nil {
		return Source{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return Source{}, errors.New("navbuild: trailing source data")
	}
	if err := source.Config.Validate(); err != nil {
		return Source{}, err
	}
	if _, err := source.Scene.Resolve(source.Config.Settings.Origin, source.Config.Strict); err != nil {
		return Source{}, err
	}
	return source, nil
}

// Build runs all pure-Go baking stages from an explicit source document.
func (source Source) Build(ctx context.Context) (*bake.Result, error) {
	return bake.Build(ctx, source.Scene, source.Config)
}

// BakeFile validates source, bakes privately, then atomically replaces the
// artifact path. A failed bake preserves the previous artifact.
func BakeFile(ctx context.Context, input, output string) (*bake.Result, error) {
	file, err := os.Open(input)
	if err != nil {
		return nil, err
	}
	source, readErr := Decode(file)
	closeErr := file.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	result, err := source.Build(ctx)
	if err != nil {
		return nil, err
	}
	encoded, err := artifact.Encode(result.Bundle)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0755); err != nil {
		return nil, err
	}
	temp, err := os.CreateTemp(filepath.Dir(output), ".golem-navigation-*")
	if err != nil {
		return nil, err
	}
	name := temp.Name()
	defer os.Remove(name)
	_, err = temp.Write(encoded)
	if err == nil {
		err = temp.Sync()
	}
	closeErr = temp.Close()
	if err != nil {
		return nil, err
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.Rename(name, output); err != nil {
		return nil, err
	}
	return result, nil
}
