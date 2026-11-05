package server

import (
	"encoding/json"

	"github.com/kamenxrider/hollis/internal/runner"
)

type samplingFields struct {
	Temperature json.RawMessage `json:"temperature"`
	TopP        json.RawMessage `json:"top_p"`
	Seed        json.RawMessage `json:"seed"`
}

func parseSampling(model string, fields samplingFields, tokens json.RawMessage, imageGeneration json.RawMessage) (*runner.GenerationOptions, error) {
	if isAbsentOrNull(fields.Temperature) && isAbsentOrNull(fields.TopP) && isAbsentOrNull(fields.Seed) && isAbsentOrNull(tokens) {
		return nil, nil
	}
	if model != string(runner.ModelLocal) || len(imageGeneration) > 0 {
		return nil, unsupportedParameter("sampling is supported only for Local model generation")
	}
	options := &runner.GenerationOptions{}
	for _, field := range []struct {
		raw json.RawMessage
		dst any
	}{
		{fields.Temperature, &options.Temperature},
		{fields.TopP, &options.TopP},
		{fields.Seed, &options.Seed},
		{tokens, &options.MaxTokens},
	} {
		if !isAbsentOrNull(field.raw) {
			if err := json.Unmarshal(field.raw, field.dst); err != nil {
				return nil, unsupportedParameter("sampling fields have invalid numeric types or ranges")
			}
		}
	}
	if err := options.Validate(); err != nil {
		return nil, unsupportedParameter(err.Error())
	}
	return options, nil
}
