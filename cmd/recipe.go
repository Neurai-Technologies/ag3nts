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
			fmt.Printf("  %-20s %-8s %s\n", r.Name, agent, r.Description)
		}
		return nil
	},
}

var recipeRunCmd = &cobra.Command{
	Use:   "run <name>",
	Short: "Execute a recipe",
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

		// Render the prompt.
		prompt, err := r.RenderPrompt(params)
		if err != nil {
			return err
		}

		fmt.Printf("Recipe:  %s\n", r.Name)
		fmt.Printf("Agent:   %s\n", r.Agent)
		if r.Model != "" {
			fmt.Printf("Model:   %s\n", r.Model)
		}
		fmt.Printf("Prompt:  %s\n", truncateStr(prompt, 200))
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
