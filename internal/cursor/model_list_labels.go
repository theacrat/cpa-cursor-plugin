package cursor

import (
	"encoding/json"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

var modelLabelFields = [2]string{"name", "display_name"}

type modelListLabel struct {
	id      string
	row     map[string]json.RawMessage
	model   pluginapi.ModelInfo
	labels  [2]string
	replace [2]bool
	managed bool
	changed bool
}

func (e *Executor) modelListLabels(rows []json.RawMessage, idKey string) []modelListLabel {
	entries := make([]modelListLabel, len(rows))
	known := make(map[string]string)
	for index, raw := range rows {
		entry := &entries[index]
		if json.Unmarshal(raw, &entry.row) != nil || entry.row == nil {
			continue
		}
		var owner string
		_ = json.Unmarshal(entry.row[idKey], &entry.id)
		_ = json.Unmarshal(entry.row["owned_by"], &owner)
		if strings.Contains(entry.id, "/") && (owner == "" || owner == "cursor") {
			entry.model, entry.managed = e.publishedMetadata(entry.id)
			if entry.managed {
				known[entry.model.ID] = entry.model.DisplayName
			}
		}
		for fieldIndex, key := range modelLabelFields {
			raw, present := entry.row[key]
			var name string
			decoded := json.Unmarshal(raw, &name) == nil && string(raw) != "null"
			entry.labels[fieldIndex] = name
			unqualified := strings.TrimSuffix(name, " ["+entry.id+"]")
			if entry.managed && (!present || decoded && familyDisplayName(unqualified, true) == entry.model.DisplayName) {
				entry.labels[fieldIndex] = entry.model.DisplayName
				entry.replace[fieldIndex] = true
			}
		}
		if entry.labels[0] == "" && entry.labels[1] == "" && !entry.managed {
			entry.labels[0] = entry.id[strings.LastIndex(entry.id, "/")+1:]
		}
	}
	for index := range entries {
		entry := &entries[index]
		if entry.managed || len(entry.row["name"]) != 0 || len(entry.row["display_name"]) != 0 {
			continue
		}
		base := entry.id[strings.LastIndex(entry.id, "/")+1:]
		if label, exists := known[base]; exists {
			entry.labels[0] = label
			if strings.HasSuffix(base, "-fast") {
				entry.labels[0] += " (Fast)"
			}
		}
	}
	for _, qualifier := range []string{"fast", "provider", "route"} {
		counts := make(map[string]map[string]bool)
		for _, entry := range entries {
			for _, label := range entry.labels {
				key := strings.ToLower(strings.Join(strings.Fields(label), " "))
				if key == "" || entry.id == "" {
					continue
				}
				if counts[key] == nil {
					counts[key] = make(map[string]bool)
				}
				counts[key][entry.id] = true
			}
		}
		for index := range entries {
			entry := &entries[index]
			for fieldIndex, label := range entry.labels {
				key := strings.ToLower(strings.Join(strings.Fields(label), " "))
				if !entry.replace[fieldIndex] || len(counts[key]) < 2 {
					continue
				}
				switch qualifier {
				case "fast":
					if strings.HasSuffix(entry.model.ID, "-fast") {
						entry.labels[fieldIndex] += " (Fast)"
					}
				case "provider":
					entry.labels[fieldIndex] += " (Cursor)"
				case "route":
					entry.labels[fieldIndex] += " [" + entry.id + "]"
				}
			}
		}
	}
	for index := range entries {
		entry := &entries[index]
		for fieldIndex, key := range modelLabelFields {
			if !entry.replace[fieldIndex] {
				continue
			}
			var previous string
			if json.Unmarshal(entry.row[key], &previous) == nil && previous == entry.labels[fieldIndex] {
				continue
			}
			entry.row[key], _ = json.Marshal(entry.labels[fieldIndex])
			entry.changed = true
		}
	}
	return entries
}
