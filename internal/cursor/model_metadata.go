package cursor

import (
	"errors"
	"strconv"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
	"google.golang.org/protobuf/encoding/protowire"
)

type availableMetadata struct {
	context        int64
	images         *bool
	tools          *bool
	contextPresent bool
}

type availableVariant struct {
	aliases        []string
	maxMode        bool
	context        string
	contextPresent bool
}

type metadataContext struct {
	value    int64
	priority int
	source   *availableMetadata
}

func availableModelInfo(raw []byte) (map[string]*availableMetadata, error) {
	models := make(map[string]*availableMetadata)
	priorities := make(map[string]int)
	contexts := make(map[string]metadataContext)
	addContext := func(id string, value int64, priority int, source *availableMetadata) {
		if id == "" {
			return
		}
		if previous, exists := contexts[id]; exists {
			if previous.priority > priority {
				return
			}
			if previous.priority == priority {
				if previous.source != source {
					value = 0
					source = nil
				} else {
					value = minKnownLimit(previous.value, value)
				}
			}
		}
		contexts[id] = metadataContext{value: value, priority: priority, source: source}
	}
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
					metadata.contextPresent = true
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
		if metadata.contextPresent {
			addContext(name, metadata.context, 3, metadata)
		}
		for _, alias := range aliases {
			add(alias, metadata, 1)
			addContext(alias, metadata.context, 1, metadata)
		}
		for _, variant := range variants {
			copy := *metadata
			if variant.maxMode {
				copy.context = maxContext
			}
			if variant.contextPresent {
				copy.context = parameterContextLimit(variant.context)
			}
			for _, alias := range variant.aliases {
				addContext(alias, copy.context, 2, metadata)
				if previous := models[alias]; previous == metadata {
					continue
				}
				add(alias, &copy, 2)
			}
		}
		return nil
	})
	for id, context := range contexts {
		if context.source == nil && models[id] == nil {
			continue
		}
		value := availableMetadata{}
		if models[id] != nil {
			value = *models[id]
		}
		value.context = context.value
		models[id] = &value
	}
	return models, err
}

func decodeAvailableVariant(raw []byte) (availableVariant, error) {
	var variant availableVariant
	err := eachWireField(raw, func(number protowire.Number, kind protowire.Type, value []byte) error {
		switch number {
		case 1:
			if kind != protowire.BytesType {
				return errors.New("invalid Cursor variant parameter")
			}
			var id, parameter string
			if err := eachWireField(value, func(number protowire.Number, kind protowire.Type, value []byte) error {
				if number != 1 && number != 2 {
					return nil
				}
				if kind != protowire.BytesType {
					return errors.New("invalid Cursor variant parameter")
				}
				if number == 1 {
					id = string(value)
				} else {
					parameter = string(value)
				}
				return nil
			}); err != nil {
				return err
			}
			if id == "context" {
				variant.context, variant.contextPresent = parameter, true
			}
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

func parameterContextLimit(value string) int64 {
	value = strings.ToLower(strings.TrimSpace(value))
	if len(value) < 2 {
		return 0
	}
	multiplier := int64(0)
	switch value[len(value)-1] {
	case 'k':
		multiplier = 1000
	case 'm':
		multiplier = 1000000
	default:
		return 0
	}
	number, err := strconv.ParseUint(value[:len(value)-1], 10, 31)
	if err != nil || number == 0 || number > uint64((1<<31-1)/multiplier) {
		return 0
	}
	return int64(number) * multiplier
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
