package cursor

import (
	"errors"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"google.golang.org/protobuf/encoding/protowire"
)

type availableMetadata struct {
	context int64
	images  *bool
	tools   *bool
}

type availableVariant struct {
	aliases []string
	maxMode bool
}

func availableModelInfo(raw []byte) (map[string]*availableMetadata, error) {
	models := make(map[string]*availableMetadata)
	priorities := make(map[string]int)
	add := func(id string, metadata *availableMetadata, priority int) {
		if id == "" {
			return
		}
		if previous, exists := models[id]; exists {
			if priorities[id] > priority || previous == metadata {
				return
			}
			if priorities[id] == priority {
				models[id] = nil
				return
			}
		}
		models[id] = metadata
		priorities[id] = priority
	}
	err := eachWireField(raw, func(number protowire.Number, kind protowire.Type, value []byte) error {
		if number != 2 || kind != protowire.BytesType {
			return nil
		}
		metadata := new(availableMetadata)
		var name string
		var aliases []string
		var variants []availableVariant
		var maxContext int64
		err := eachWireField(value, func(number protowire.Number, kind protowire.Type, value []byte) error {
			switch number {
			case 1, 18, 36, 37:
				if kind != protowire.BytesType {
					return errors.New("invalid Cursor metadata identity")
				}
				if number == 1 {
					name = string(value)
				} else {
					aliases = append(aliases, string(value))
				}
			case 5, 10, 15, 16:
				if kind != protowire.VarintType {
					return errors.New("invalid Cursor metadata value")
				}
				v, n := protowire.ConsumeVarint(value)
				if n < 0 || (number == 5 || number == 10) && v > 1 || (number == 15 || number == 16) && v > 1<<31-1 {
					return errors.New("invalid Cursor metadata value")
				}
				switch number {
				case 5:
					metadata.tools = boolPointer(v == 1)
				case 10:
					metadata.images = boolPointer(v == 1)
				case 15:
					metadata.context = int64(v)
				case 16:
					maxContext = int64(v)
				}
			case 30:
				if kind != protowire.BytesType {
					return errors.New("invalid Cursor metadata variant")
				}
				variant, err := decodeAvailableVariant(value)
				if err != nil {
					return err
				}
				variants = append(variants, variant)
			}
			return nil
		})
		if err != nil {
			return err
		}
		add(name, metadata, 2)
		for _, alias := range aliases {
			add(alias, metadata, 1)
		}
		for _, variant := range variants {
			copy := *metadata
			if variant.maxMode {
				copy.context = maxContext
			}
			for _, alias := range variant.aliases {
				if previous := models[alias]; previous == metadata {
					continue
				}
				add(alias, &copy, 2)
			}
		}
		return nil
	})
	return models, err
}

func decodeAvailableVariant(raw []byte) (availableVariant, error) {
	var variant availableVariant
	err := eachWireField(raw, func(number protowire.Number, kind protowire.Type, value []byte) error {
		switch number {
		case 11:
			if kind != protowire.BytesType {
				return errors.New("invalid Cursor variant identity")
			}
			variant.aliases = append(variant.aliases, string(value))
		case 3:
			v, n := protowire.ConsumeVarint(value)
			if kind != protowire.VarintType || n < 0 || v > 1 {
				return errors.New("invalid Cursor variant mode")
			}
			variant.maxMode = v == 1
		}
		return nil
	})
	return variant, err
}

func boolPointer(value bool) *bool { return &value }

func applyAvailableMetadata(model *pluginapi.ModelInfo, group *variantGroup, metadata map[string]*availableMetadata) {
	context := int64(0)
	images, tools := true, true
	for _, id := range group.ids {
		extra, exact := metadata[id]
		if !exact {
			base, _, _, _ := analyseVariant(id)
			extra = metadata[base]
		}
		if extra == nil {
			return
		}
		if extra.context <= 0 {
			context = -1
		} else if context == 0 || context > extra.context {
			context = extra.context
		}
		images = images && extra.images != nil && *extra.images
		tools = tools && extra.tools != nil && *extra.tools
	}
	if context > 0 {
		model.ContextLength = context
		model.InputTokenLimit = context
	}
	if images {
		model.SupportedInputModalities = []string{"text", "image"}
	}
	if tools {
		model.SupportedParameters = []string{"tools", "tool_choice"}
	}
}
