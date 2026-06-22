package report

import (
	"encoding/json"

	"github.com/jackchuka/postura/internal/engine"
)

// SARIF renders fail and unknown findings as a SARIF 2.1.0 log, uploadable to GitHub
// code scanning and usable as an audit evidence artifact.
func SARIF(fs []engine.Finding) ([]byte, error) {
	driver := sarifDriver{Name: "postura", InformationURI: "https://github.com/jackchuka/postura"}
	var results []sarifResult
	seenRule := map[string]bool{}

	for _, f := range fs {
		if f.Status == engine.StatusPass {
			continue
		}
		if !seenRule[f.ID] {
			seenRule[f.ID] = true
			driver.Rules = append(driver.Rules, sarifRule{
				ID:               f.ID,
				ShortDescription: sarifText{Text: f.Title},
				HelpURI:          "",
				DefaultConfig:    sarifConfig{Level: level(f.Severity)},
			})
		}
		lvl := level(f.Severity)
		msg := f.Title
		if f.Status == engine.StatusUnknown {
			lvl = "note"
			msg = "unknown: " + f.Title
		}
		if f.Notes != "" {
			msg += " (" + f.Notes + ")"
		}
		results = append(results, sarifResult{
			RuleID:  f.ID,
			Level:   lvl,
			Message: sarifText{Text: f.Target + ": " + msg},
			Locations: []sarifLocation{{
				LogicalLocations: []sarifLogicalLoc{{Name: f.Target, Kind: f.Scope}},
			}},
		})
	}

	log := sarifLog{
		Schema:  "https://json.schemastore.org/sarif-2.1.0.json",
		Version: "2.1.0",
		Runs:    []sarifRun{{Tool: sarifTool{Driver: driver}, Results: results}},
	}
	return json.MarshalIndent(log, "", "  ")
}

// level maps a rule severity to a SARIF level (error|warning|note).
func level(severity string) string {
	switch severity {
	case "error":
		return "error"
	case "warn":
		return "warning"
	default:
		return "note"
	}
}

type sarifLog struct {
	Schema  string     `json:"$schema"`
	Version string     `json:"version"`
	Runs    []sarifRun `json:"runs"`
}
type sarifRun struct {
	Tool    sarifTool     `json:"tool"`
	Results []sarifResult `json:"results"`
}
type sarifTool struct {
	Driver sarifDriver `json:"driver"`
}
type sarifDriver struct {
	Name           string      `json:"name"`
	InformationURI string      `json:"informationUri,omitempty"`
	Rules          []sarifRule `json:"rules"`
}
type sarifRule struct {
	ID               string      `json:"id"`
	ShortDescription sarifText   `json:"shortDescription"`
	HelpURI          string      `json:"helpUri,omitempty"`
	DefaultConfig    sarifConfig `json:"defaultConfiguration"`
}
type sarifConfig struct {
	Level string `json:"level"`
}
type sarifResult struct {
	RuleID    string          `json:"ruleId"`
	Level     string          `json:"level"`
	Message   sarifText       `json:"message"`
	Locations []sarifLocation `json:"locations,omitempty"`
}
type sarifLocation struct {
	LogicalLocations []sarifLogicalLoc `json:"logicalLocations,omitempty"`
}
type sarifLogicalLoc struct {
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"`
}
type sarifText struct {
	Text string `json:"text"`
}
