package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/rohanrgit/ag3nts/internal/recipe"
)

var recipeParams []string

var recipeCmd = &cobra.Command{
	Use:   "recipe",
	Short: "Manage and run declarative recipes",
	Long: `Recipes are YAML files that bundle a persona, preferred agent, tools,
and parameters into a reusable task definition.

  ag3nts recipe list                         # show available recipes
  ag3nts recipe run code-review --param target=./src
  ag3nts recipe validate my-recipe.yaml`,
}

var recipeListCmd = &cobra.Command{
	Use:   "list",
	Short: "List available recipes",
	RunE: func(cmd *cobra.Command, args []string) error {
		loader := buildRecipeLoader()
		recipes := loader.List()

		if len(recipes) == 0 {
			fmt.Println("No recipes found. Add .yaml files to config/recipes/")
			return nil
		}

		for _, r := range recipes {
			agent := r.Agent
			if agent == "" {
				agent = "any"
			}
			kind := "single"
			if r.IsMultiTask() {
				kind = fmt.Sprintf("multi(%d)", len(r.Tasks))
			}
			fmt.Printf("  %-20s %-10s %-8s %s\n", r.Name, kind, agent, r.Description)
		}
		return nil
	},
}

var recipeRunCmd = &cobra.Command{
	Use:   "run <name>",
	Short: "Show recipe dispatch plan (run it from the orchestrator TUI)",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		loader := buildRecipeLoader()

		r, err := loader.Get(name)
		if err != nil {
			return err
		}
		if err := r.Validate(); err != nil {
			return fmt.Errorf("invalid recipe: %w", err)
		}

		// Parse --param key=value flags.
		params := make(map[string]string)
		for _, p := range recipeParams {
			parts := strings.SplitN(p, "=", 2)
			if len(parts) != 2 {
				return fmt.Errorf("invalid param format %q (expected key=value)", p)
			}
			params[parts[0]] = parts[1]
		}

		fmt.Printf("Recipe: %s\n", r.Name)

		if r.IsMultiTask() {
			fmt.Printf("Kind:   multi-task (%d sub-tasks)\n", len(r.Tasks))
			fmt.Println()
			fmt.Println("Sub-tasks (in expansion order):")
			for i, st := range r.Tasks {
				ag := st.Agent
				if ag == "" {
					ag = "auto"
				}
				evalMark := ""
				if st.EvaluatorOf != "" {
					evalMark = fmt.Sprintf(" [evaluator-of: %s]", st.EvaluatorOf)
				}
				deps := ""
				if len(st.DependsOn) > 0 {
					deps = fmt.Sprintf(" (depends: %s)", strings.Join(st.DependsOn, ", "))
				}
				fmt.Printf("  %d. %-12s %-10s%s%s\n", i+1, st.ID, ag, deps, evalMark)
			}
		} else {
			fmt.Printf("Kind:   single-task\n")
			fmt.Printf("Agent:  %s\n", r.Agent)
			if r.Model != "" {
				fmt.Printf("Model:  %s\n", r.Model)
			}
			prompt, err := r.RenderPrompt(params)
			if err != nil {
				return err
			}
			fmt.Printf("Prompt: %s\n", truncateStr(prompt, 200))
		}

		fmt.Println()
		fmt.Println("Run this recipe inside the orchestrator TUI with:")
		fmt.Printf("  /recipe %s", name)
		for k, v := range params {
			fmt.Printf(" %s=%s", k, v)
		}
		fmt.Println()
		return nil
	},
}

var recipeValidateCmd = &cobra.Command{
	Use:   "validate <path>",
	Short: "Validate a recipe YAML file",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := recipe.LoadRecipe(args[0])
		if err != nil {
			return err
		}
		if err := r.Validate(); err != nil {
			return err
		}
		fmt.Printf("Recipe %q is valid (%d parameters, agent=%s)\n", r.Name, len(r.Parameters), r.Agent)
		return nil
	},
}

func init() {
	recipeRunCmd.Flags().StringArrayVar(&recipeParams, "param", nil, "recipe parameter (key=value)")
	recipeCmd.AddCommand(recipeListCmd)
	recipeCmd.AddCommand(recipeRunCmd)
	recipeCmd.AddCommand(recipeValidateCmd)
	rootCmd.AddCommand(recipeCmd)
}

func buildRecipeLoader() *recipe.Loader {
	var paths []string
	if layout != nil {
		// Project-local recipes.
		paths = append(paths, layout.Base+"/config/recipes")
		// Active workflow recipes.
		if cfg != nil && cfg.Workflows.Active != "" {
			paths = append(paths, layout.Base+"/config/workflows/"+cfg.Workflows.Active+"/recipes")
		}
	}
	return recipe.NewLoader(paths...)
}

func truncateStr(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
