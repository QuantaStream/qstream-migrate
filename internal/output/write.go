package output

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/QuantaStream/qstream-migrate/internal/model"
	"gopkg.in/yaml.v3"
)

type Paths struct {
	Inventory string
	Plan      string
	Readme    string
}

func WriteAnalysis(outDir string, inv model.Inventory, plan model.MigrationPlan) (Paths, error) {
	if outDir == "" {
		outDir = "migration-plan"
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return Paths{}, fmt.Errorf("create output directory: %w", err)
	}

	paths := Paths{
		Inventory: filepath.Join(outDir, "inventory.json"),
		Plan:      filepath.Join(outDir, "plan.yaml"),
		Readme:    filepath.Join(outDir, "README.md"),
	}

	inventoryBytes, err := json.MarshalIndent(inv, "", "  ")
	if err != nil {
		return Paths{}, fmt.Errorf("encode inventory: %w", err)
	}
	if err := os.WriteFile(paths.Inventory, append(inventoryBytes, '\n'), 0o644); err != nil {
		return Paths{}, fmt.Errorf("write inventory: %w", err)
	}

	planBytes, err := yaml.Marshal(plan)
	if err != nil {
		return Paths{}, fmt.Errorf("encode plan: %w", err)
	}
	if err := os.WriteFile(paths.Plan, planBytes, 0o644); err != nil {
		return Paths{}, fmt.Errorf("write plan: %w", err)
	}

	if err := os.WriteFile(paths.Readme, []byte(renderReadme(inv, plan)), 0o644); err != nil {
		return Paths{}, fmt.Errorf("write analysis README: %w", err)
	}
	return paths, nil
}

func renderReadme(inv model.Inventory, plan model.MigrationPlan) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# qstream-migrate Analysis\n\n")
	fmt.Fprintf(&b, "Generated at: `%s`\n\n", plan.GeneratedAt.Format("2006-01-02T15:04:05Z07:00"))
	fmt.Fprintf(&b, "Source: `%s` schema `%s`\n\n", plan.Source.Kind, plan.Source.Schema)
	fmt.Fprintf(&b, "## Files\n\n")
	fmt.Fprintf(&b, "- `inventory.json`: raw MySQL metadata plus collected column profiles.\n")
	fmt.Fprintf(&b, "- `plan.yaml`: editable QuantaStream migration recommendations.\n")
	fmt.Fprintf(&b, "- `README.md`: this summary.\n\n")
	fmt.Fprintf(&b, "## Summary\n\n")
	fmt.Fprintf(&b, "- tables analyzed: %d\n", len(plan.Tables))
	fmt.Fprintf(&b, "- fields analyzed: %d\n", countFields(plan))
	fmt.Fprintf(&b, "- relationships and candidates: %d\n", countRelationships(plan))
	fmt.Fprintf(&b, "- time partitioning reviews: %d\n\n", countTimeQuantumReviews(plan))
	fmt.Fprintf(&b, "## Scope\n\n")
	fmt.Fprintf(&b, "This analysis is a MySQL-to-QuantaStream migration plan. The generated schema is reviewable output, not a one-click replacement for human schema design. Review partitioning, relationship modeling, string mapper choices, and free-text fields before loading production data.\n\n")
	fmt.Fprintf(&b, "## Next Steps\n\n")
	fmt.Fprintf(&b, "1. Review `plan.yaml` and decide which tables and fields to include.\n")
	fmt.Fprintf(&b, "2. Review any fields with `recommended_mapping_strategy: Review` or `ReviewText`.\n")
	fmt.Fprintf(&b, "3. Confirm primary keys and relationships before generating QuantaStream schemas.\n")
	fmt.Fprintf(&b, "4. Review `time_quantum` candidates on date/time tables and set a field when time partitioning should be emitted.\n")
	fmt.Fprintf(&b, "5. Run `qstream-migrate check --plan plan.yaml` until the review items are understood.\n")
	fmt.Fprintf(&b, "6. Run `qstream-migrate generate --plan plan.yaml --out configuration` once the plan looks right.\n")
	fmt.Fprintf(&b, "7. Optionally run `qstream-migrate compare-schema` against an existing curated QuantaStream configuration.\n")
	fmt.Fprintf(&b, "8. Use `qstream-migrate load-plan`, `export mysql`, `post-jsonl`, and `validate counts` to move data and verify row counts.\n\n")
	if len(inv.Tables) > 0 {
		fmt.Fprintf(&b, "## Tables\n\n")
		for _, table := range inv.Tables {
			fmt.Fprintf(&b, "- `%s`: %d columns", table.Name, len(table.Columns))
			if len(table.PrimaryKey) > 0 {
				fmt.Fprintf(&b, ", primary key `%s`", strings.Join(table.PrimaryKey, ", "))
			}
			if tablePlan, ok := tablePlanByName(plan, table.Name); ok && tablePlan.TimeQuantum != nil {
				fmt.Fprintf(&b, ", time candidates `%s`", strings.Join(tablePlan.TimeQuantum.CandidateFields, ", "))
			}
			fmt.Fprintf(&b, "\n")
		}
	}
	return b.String()
}

func countFields(plan model.MigrationPlan) int {
	var count int
	for _, table := range plan.Tables {
		count += len(table.Fields)
	}
	return count
}

func countTimeQuantumReviews(plan model.MigrationPlan) int {
	var count int
	for _, table := range plan.Tables {
		if table.TimeQuantum != nil {
			count++
		}
	}
	return count
}

func tablePlanByName(plan model.MigrationPlan, name string) (model.TablePlan, bool) {
	for _, table := range plan.Tables {
		if table.Name == name {
			return table, true
		}
	}
	return model.TablePlan{}, false
}

func countRelationships(plan model.MigrationPlan) int {
	var count int
	for _, table := range plan.Tables {
		count += len(table.Relationships)
	}
	return count
}
