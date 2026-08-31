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
	fmt.Fprintf(&b, "- relationships and candidates: %d\n\n", countRelationships(plan))
	fmt.Fprintf(&b, "## Next Steps\n\n")
	fmt.Fprintf(&b, "1. Review `plan.yaml` and decide which tables and fields to include.\n")
	fmt.Fprintf(&b, "2. Review any fields with `recommended_mapping_strategy: Review` or `ReviewText`.\n")
	fmt.Fprintf(&b, "3. Confirm primary keys and relationships before generating QuantaStream schemas.\n")
	fmt.Fprintf(&b, "4. Run the future `qstream-migrate generate` step once the plan looks right.\n\n")
	if len(inv.Tables) > 0 {
		fmt.Fprintf(&b, "## Tables\n\n")
		for _, table := range inv.Tables {
			fmt.Fprintf(&b, "- `%s`: %d columns", table.Name, len(table.Columns))
			if len(table.PrimaryKey) > 0 {
				fmt.Fprintf(&b, ", primary key `%s`", strings.Join(table.PrimaryKey, ", "))
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

func countRelationships(plan model.MigrationPlan) int {
	var count int
	for _, table := range plan.Tables {
		count += len(table.Relationships)
	}
	return count
}
