package acceptance

import (
	"fmt"
	"slices"
)

type Milestone uint8

const (
	MilestoneM1 Milestone = iota + 1
	MilestoneM2
	MilestoneM3
	MilestoneM4
	MilestoneM5
	MilestoneM6
)

type CoverageDimensionID string
type CoverageValue string

type CoverageDimension struct {
	ID        CoverageDimensionID
	Milestone Milestone
	Values    []CoverageValue
}
type CoverageSpec struct {
	Milestone  Milestone
	Dimensions []CoverageDimension
}
type CoverageAssignment struct {
	Dimension CoverageDimensionID
	Value     CoverageValue
}
type CoverageResult struct {
	Dimension CoverageDimensionID
	Milestone Milestone
	Gated     bool
	Uncovered []CoverageValue
}

func EvaluateCoverage(spec CoverageSpec, run Milestone, assignments []CoverageAssignment) ([]CoverageResult, error) {
	if spec.Milestone < MilestoneM1 || spec.Milestone > MilestoneM6 || run < MilestoneM1 || run > MilestoneM6 || len(spec.Dimensions) == 0 || len(spec.Dimensions) > 32 {
		return nil, fmt.Errorf("coverage spec requires milestones M1..M6 and 1..32 dimensions")
	}
	seenDimensions := map[CoverageDimensionID]bool{}
	assigned := map[CoverageDimensionID]map[CoverageValue]bool{}
	for _, a := range assignments {
		if assigned[a.Dimension] == nil {
			assigned[a.Dimension] = map[CoverageValue]bool{}
		}
		assigned[a.Dimension][a.Value] = true
	}
	results := make([]CoverageResult, 0, len(spec.Dimensions))
	for _, dimension := range spec.Dimensions {
		if dimension.ID == "" || seenDimensions[dimension.ID] || dimension.Milestone < spec.Milestone || dimension.Milestone > MilestoneM6 || len(dimension.Values) == 0 || len(dimension.Values) > 64 {
			return nil, fmt.Errorf("coverage dimension %q is invalid, duplicate, out of milestone order, or outside the 1..64 value bound", dimension.ID)
		}
		seenDimensions[dimension.ID] = true
		result := CoverageResult{Dimension: dimension.ID, Milestone: dimension.Milestone, Gated: run >= dimension.Milestone}
		values := map[CoverageValue]bool{}
		for _, value := range dimension.Values {
			if value == "" || values[value] {
				return nil, fmt.Errorf("coverage dimension %q has an empty or duplicate value", dimension.ID)
			}
			values[value] = true
			if !assigned[dimension.ID][value] {
				result.Uncovered = append(result.Uncovered, value)
			}
		}
		for value := range assigned[dimension.ID] {
			if !values[value] {
				return nil, fmt.Errorf("coverage assignment %q=%q is outside the declared closed value set", dimension.ID, value)
			}
		}
		slices.Sort(result.Uncovered)
		results = append(results, result)
	}
	return results, nil
}

func RequireCovered(results []CoverageResult) error {
	for _, result := range results {
		if result.Gated && len(result.Uncovered) > 0 {
			return fmt.Errorf("coverage dimension %q at milestone M%d is missing values %v", result.Dimension, result.Milestone, result.Uncovered)
		}
	}
	return nil
}
