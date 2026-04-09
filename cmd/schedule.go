package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/rohanrgit/ag3nts/internal/store"
)

var (
	schedName   string
	schedCron   string
	schedRecipe string
)

var scheduleCmd = &cobra.Command{
	Use:   "schedule",
	Short: "Manage cron-based background schedules",
	Long: `Schedules run recipes automatically on a cron schedule.

  ag3nts schedule add --name "Weekly Audit" --cron "0 2 * * 0" --recipe repo-audit
  ag3nts schedule list
  ag3nts schedule remove <id>
  ag3nts schedule run <id>`,
}

var scheduleAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a new schedule",
	RunE: func(cmd *cobra.Command, args []string) error {
		if schedName == "" || schedCron == "" || schedRecipe == "" {
			return fmt.Errorf("--name, --cron, and --recipe are all required")
		}

		// Validate recipe exists.
		loader := buildRecipeLoader()
		if _, err := loader.Get(schedRecipe); err != nil {
			return fmt.Errorf("recipe %q: %w", schedRecipe, err)
		}

		// Open store.
		db, err := openScheduleDB()
		if err != nil {
			return err
		}
		defer db.Close()

		id := fmt.Sprintf("sched_%d", time.Now().UnixNano()%100000)
		rec := &store.ScheduleRecord{
			ID:        id,
			Name:      schedName,
			Cron:      schedCron,
			Recipe:    schedRecipe,
			Enabled:   true,
			CreatedAt: time.Now().UTC(),
		}
		if err := db.CreateSchedule(rec); err != nil {
			return err
		}

		fmt.Printf("Schedule added: %s (%s)\n", id, schedName)
		fmt.Printf("  Cron:   %s\n", schedCron)
		fmt.Printf("  Recipe: %s\n", schedRecipe)
		fmt.Println("\nSchedule will activate next time the orchestrator starts.")
		return nil
	},
}

var scheduleListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all schedules",
	RunE: func(cmd *cobra.Command, args []string) error {
		db, err := openScheduleDB()
		if err != nil {
			return err
		}
		defer db.Close()

		schedules, err := db.ListSchedules()
		if err != nil {
			return err
		}

		if len(schedules) == 0 {
			fmt.Println("No schedules configured. Use 'ag3nts schedule add' to create one.")
			return nil
		}

		for _, s := range schedules {
			enabled := "enabled"
			if !s.Enabled {
				enabled = "disabled"
			}
			lastRun := "never"
			if !s.LastRun.IsZero() {
				lastRun = s.LastRun.Format("2006-01-02 15:04")
			}
			nextRun := "—"
			if !s.NextRun.IsZero() {
				nextRun = s.NextRun.Format("2006-01-02 15:04")
			}
			fmt.Printf("  %-20s %-10s %-18s recipe=%-15s last=%s  next=%s\n",
				s.ID, enabled, s.Cron, s.Recipe, lastRun, nextRun)
			if s.Name != "" {
				fmt.Printf("  %-20s %s\n", "", s.Name)
			}
		}
		return nil
	},
}

var scheduleRemoveCmd = &cobra.Command{
	Use:   "remove <id>",
	Short: "Remove a schedule",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		db, err := openScheduleDB()
		if err != nil {
			return err
		}
		defer db.Close()

		if err := db.DeleteSchedule(args[0]); err != nil {
			return err
		}
		fmt.Printf("Schedule %s removed.\n", args[0])
		return nil
	},
}

var scheduleRunCmd = &cobra.Command{
	Use:   "run <id>",
	Short: "Trigger a schedule immediately",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		db, err := openScheduleDB()
		if err != nil {
			return err
		}
		defer db.Close()

		rec, err := db.GetSchedule(args[0])
		if err != nil {
			return err
		}
		if rec == nil {
			return fmt.Errorf("schedule %q not found", args[0])
		}

		// Load and render the recipe to show what would run.
		loader := buildRecipeLoader()
		r, err := loader.Get(rec.Recipe)
		if err != nil {
			return fmt.Errorf("recipe %q: %w", rec.Recipe, err)
		}

		_ = r // recipe loaded successfully
		fmt.Printf("Schedule %s (%s) — recipe: %s\n", rec.ID, rec.Name, rec.Recipe)
		fmt.Println("To execute immediately, run this inside the orchestrator TUI.")
		fmt.Printf("  /recipe %s\n", rec.Recipe)
		return nil
	},
}

func init() {
	scheduleAddCmd.Flags().StringVar(&schedName, "name", "", "schedule display name")
	scheduleAddCmd.Flags().StringVar(&schedCron, "cron", "", "cron expression (5-field)")
	scheduleAddCmd.Flags().StringVar(&schedRecipe, "recipe", "", "recipe name to execute")

	scheduleCmd.AddCommand(scheduleAddCmd)
	scheduleCmd.AddCommand(scheduleListCmd)
	scheduleCmd.AddCommand(scheduleRemoveCmd)
	scheduleCmd.AddCommand(scheduleRunCmd)
	rootCmd.AddCommand(scheduleCmd)
}

// buildRecipeLoader is defined in cmd/recipe.go — reused here.

func openScheduleDB() (*store.DB, error) {
	if layout == nil {
		return nil, fmt.Errorf("ag3nts project not detected")
	}
	return store.Open(store.Config{Path: layout.State + "/ag3nts.db"})
}
