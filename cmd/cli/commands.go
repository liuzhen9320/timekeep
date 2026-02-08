package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/jms-guy/timekeep/internal/database"
)

// Adds programs into the database, and sends communication to service to being tracking them
func (s *CLIService) AddPrograms(ctx context.Context, args []string, category, project string) error {
	categoryNull := sql.NullString{
		String: category,
		Valid:  category != "",
	}

	projectNull := sql.NullString{
		String: project,
		Valid:  project != "",
	}

	for _, program := range args {
		err := s.PrRepo.AddProgram(ctx, database.AddProgramParams{
			Name:     strings.ToLower(program),
			Category: categoryNull,
			Project:  projectNull,
		})
		if err != nil {
			return fmt.Errorf("error adding program %s: %w", program, err)
		}
	}

	err := s.ServiceCmd.WriteToService()
	if err != nil {
		return fmt.Errorf("programs added but failed to notify service: %w", err)
	}

	return nil
}

// Update program's category/project fields and notify service of change
func (s *CLIService) UpdateProgram(ctx context.Context, args []string, category, project string) error {
	program := args[0]

	if category != "" {
		err := s.PrRepo.UpdateCategory(ctx, database.UpdateCategoryParams{
			Category: sql.NullString{String: category, Valid: true},
			Name:     program,
		})
		if err != nil {
			return fmt.Errorf("error updating program category: %w", err)
		}
	}

	if project != "" {
		err := s.PrRepo.UpdateProject(ctx, database.UpdateProjectParams{
			Project: sql.NullString{String: project, Valid: true},
			Name:    program,
		})
		if err != nil {
			return fmt.Errorf("error updating program project: %w", err)
		}
	}

	err := s.ServiceCmd.WriteToService()
	if err != nil {
		return fmt.Errorf("programs updated but failed to notify service: %w", err)
	}

	return nil
}

// Removes programs from database, and tells service to stop tracking them
func (s *CLIService) RemovePrograms(ctx context.Context, args []string, all bool) error {
	if all {
		err := s.PrRepo.RemoveAllPrograms(ctx)
		if err != nil {
			return fmt.Errorf("error removing all programs: %w", err)
		}

		err = s.ServiceCmd.WriteToService()
		if err != nil {
			return fmt.Errorf("error alerting service of program removal: %w", err)
		}

		return nil
	}

	if len(args) < 1 {
		return fmt.Errorf("missing argument")
	}

	for _, program := range args {
		err := s.PrRepo.RemoveProgram(ctx, strings.ToLower(program))
		if err != nil {
			return fmt.Errorf("error removing program %s: %w", program, err)
		}
	}

	err := s.ServiceCmd.WriteToService()
	if err != nil {
		return fmt.Errorf("programs removed but failed to notify service: %w", err)
	}

	return nil
}

// Prints a list of programs currently being tracked by service
func (s *CLIService) GetList(ctx context.Context) error {
	programs, err := s.PrRepo.GetAllProgramNames(ctx)
	if err != nil {
		return fmt.Errorf("error getting list of programs: %w", err)
	}

	if len(programs) == 0 {
		return nil
	}

	for _, program := range programs {
		fmt.Printf(" • %s\n", program)
	}

	return nil
}

// Return basic list of all programs being tracked and their current lifetime in minutes
func (s *CLIService) GetAllInfo(ctx context.Context) error {
	programs, err := s.PrRepo.GetAllPrograms(ctx)
	if err != nil {
		return fmt.Errorf("error getting programs list: %w", err)
	}

	if len(programs) == 0 {
		return nil
	}

	for _, program := range programs {
		duration := time.Duration(program.LifetimeSeconds) * time.Second

		if duration < time.Minute {
			fmt.Printf("  %s: %d seconds\n", program.Name, int(duration.Seconds()))
		} else if duration < time.Hour {
			fmt.Printf("  %s: %d minutes\n", program.Name, int(duration.Minutes()))
		} else {
			hours := int(duration.Hours())
			minutes := int(duration.Minutes()) % 60
			fmt.Printf("  %s: %dh %dm\n", program.Name, hours, minutes)
		}
	}

	return nil
}

// Get detailed stats for a single tracked program
func (s *CLIService) GetInfo(ctx context.Context, args []string) error {
	program, err := s.PrRepo.GetProgramByName(ctx, strings.ToLower(args[0]))
	if err != nil {
		return fmt.Errorf("error getting tracked program: %w", err)
	}

	duration := time.Duration(program.LifetimeSeconds) * time.Second

	lastSession, err := s.HsRepo.GetLastSessionForProgram(ctx, program.Name)
	if err != nil {
		if err == sql.ErrNoRows {
			if program.Category.String != "" {
				fmt.Printf(" • Category: %s\n", program.Category.String)
			}
			if program.Project.String != "" {
				fmt.Printf(" • Project: %s\n", program.Project.String)
			}
			s.formatDuration(" • Current Lifetime: ", duration)
			fmt.Printf(" • Total sessions to date: 0\n")
			fmt.Printf(" • Last Session: None\n")
			return nil
		} else {
			return fmt.Errorf("error getting last session for %s: %w", program.Name, err)
		}
	}

	sessionCount, err := s.HsRepo.GetCountOfSessionsForProgram(ctx, program.Name)
	if err != nil {
		return fmt.Errorf("error getting history count for %s: %w", program.Name, err)
	}

	if program.Category.String != "" {
		fmt.Printf(" • Category: %s\n", program.Category.String)
	}
	if program.Project.String != "" {
		fmt.Printf(" • Project: %s\n", program.Project.String)
	}
	s.formatDuration(" • Current Lifetime: ", duration)
	fmt.Printf(" • Total sessions to date: %d\n", sessionCount)

	lastDuration := time.Duration(lastSession.DurationSeconds) * time.Second
	fmt.Printf(" • Last Session: %s - %s ",
		lastSession.StartTime.Format("2006-01-02 15:04"),
		lastSession.EndTime.Format("2006-01-02 15:04"))
	s.formatDuration("(", lastDuration)
	fmt.Printf(")\n")

	if sessionCount > 0 {
		avgSeconds := program.LifetimeSeconds / sessionCount
		avgDuration := time.Duration(avgSeconds) * time.Second
		s.formatDuration(" • Average session length: ", avgDuration)
	}

	return nil
}

// Returns session history for a given program
func (s *CLIService) GetSessionHistory(ctx context.Context, args []string, date, start, end string, limit int64) error {
	programName := ""
	if len(args) != 0 {
		programName = args[0]
	}

	var history []database.SessionHistory
	var err error

	if programName == "" {
		history, err = s.getSessionHistoryNoName(ctx, date, start, end, limit)
		if err != nil {
			return err
		}
	} else {
		history, err = s.getSessionHistoryNamed(ctx, programName, date, start, end, limit)
		if err != nil {
			return err
		}
	}

	if len(history) == 0 {
		return nil
	}

	for _, session := range history {
		printSession(session)
	}

	return nil
}

// Reset tracked program session records
func (s *CLIService) ResetStats(ctx context.Context, args []string, all bool) error {
	if all {
		err := s.ResetAllDatabase(ctx)
		if err != nil {
			return err
		}

	} else {
		if len(args) == 0 {
			fmt.Println("No arguments given to reset")
			return nil
		}

		for _, program := range args {
			err := s.ResetDatabaseForProgram(ctx, strings.ToLower(program))
			if err != nil {
				return err
			}
		}

	}

	err := s.ServiceCmd.WriteToService()
	if err != nil {
		fmt.Printf("Warning: Failed to notify service: %v\n", err)
	}

	return nil
}

// Removes active session and session records for all programs
func (s *CLIService) ResetAllDatabase(ctx context.Context) error {
	err := s.AsRepo.RemoveAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("error removing all active sessions: %w", err)
	}
	err = s.HsRepo.RemoveAllRecords(ctx)
	if err != nil {
		return fmt.Errorf("error removing all session records: %w", err)
	}
	err = s.PrRepo.ResetAllLifetimes(ctx)
	if err != nil {
		return fmt.Errorf("error resetting lifetime values: %w", err)
	}

	return nil
}

// Removes Removes active session and session records for single program
func (s *CLIService) ResetDatabaseForProgram(ctx context.Context, program string) error {
	program = strings.ToLower(program)

	err := s.AsRepo.RemoveActiveSession(ctx, program)
	if err != nil {
		return fmt.Errorf("error removing active session for %s: %w", program, err)
	}
	err = s.HsRepo.RemoveRecordsForProgram(ctx, program)
	if err != nil {
		return fmt.Errorf("error removing session records for %s: %w", program, err)
	}
	err = s.PrRepo.ResetLifetimeForProgram(ctx, program)
	if err != nil {
		return fmt.Errorf("error resetting lifetime for %s: %w", program, err)
	}

	return nil
}

// Prints a list of currently active sessions being tracked by service
func (s *CLIService) GetActiveSessions(ctx context.Context) error {
	activeSessions, err := s.AsRepo.GetAllActiveSessions(ctx)
	if err != nil {
		return fmt.Errorf("error getting active sessions: %w", err)
	}
	if len(activeSessions) == 0 {
		return nil
	}

	for _, session := range activeSessions {
		duration := time.Since(session.StartTime)
		sessionDetails := fmt.Sprintf(" • %s - ", session.ProgramName)

		s.formatDuration(sessionDetails, duration)
	}

	return nil
}

// Clears all active sessions and resets the count
func (s *CLIService) CleanActiveSessions(ctx context.Context) error {
	err := s.AsRepo.RemoveAllSessions(ctx)
	if err != nil {
		return fmt.Errorf("error removing all active sessions: %w", err)
	}
	fmt.Println("All active sessions cleared successfully")
	return nil
}

// Basic function to print the current Timekeep version
func (s *CLIService) GetVersion() error {
	fmt.Println(s.Version)
	return nil
}

// Changes config to enable WakaTime
func (s *CLIService) EnableWakaTime(apiKey, path string) error {
	if s.Config.WakaTime.Enabled {
		return nil
	}

	if apiKey != "" {
		s.Config.WakaTime.APIKey = apiKey
	}

	if s.Config.WakaTime.APIKey == "" {
		return fmt.Errorf("WakaTime API key required. Use flag: --api-key <key>")
	}

	if path != "" {
		s.Config.WakaTime.CLIPath = path
	}

	if s.Config.WakaTime.CLIPath == "" {
		return fmt.Errorf("wakatime-cli path required. Use flag: --set-path <path>")
	}

	s.Config.WakaTime.Enabled = true

	if err := s.saveAndNotify(); err != nil {
		return err
	}

	return nil
}

// Disables WakaTime in config
func (s *CLIService) DisableWakaTime() error {
	if !s.Config.WakaTime.Enabled {
		return nil
	}

	s.Config.WakaTime.Enabled = false

	if err := s.saveAndNotify(); err != nil {
		return err
	}

	return nil
}

// Changes config to enable Wakapi
func (s *CLIService) EnableWakapi(apiKey, server string) error {
	if s.Config.Wakapi.Enabled {
		return nil
	}

	if apiKey != "" {
		s.Config.Wakapi.APIKey = apiKey
	}

	if s.Config.Wakapi.APIKey == "" {
		return fmt.Errorf("WakaTime API key required. Use flag: --api_key <key>")
	}

	if server != "" {
		s.Config.Wakapi.Server = server
	}

	if s.Config.Wakapi.Server == "" {
		return fmt.Errorf("wakapi server address required. Use flag: --server <address>")
	}

	s.Config.Wakapi.Enabled = true

	if err := s.saveAndNotify(); err != nil {
		return err
	}

	return nil
}

// Disables Wakapi in config
func (s *CLIService) DisableWakapi() error {
	if !s.Config.Wakapi.Enabled {
		return nil
	}

	s.Config.Wakapi.Enabled = false

	if err := s.saveAndNotify(); err != nil {
		return err
	}

	return nil
}

// Set various config values
func (s *CLIService) SetConfig(cliPath, server, project, interval string, grace int) error {
	if cliPath != "" {
		s.Config.WakaTime.CLIPath = cliPath
	}
	if server != "" {
		s.Config.Wakapi.Server = server
	}
	if project != "" {
		s.Config.WakaTime.GlobalProject = project
		s.Config.Wakapi.GlobalProject = project
	}
	if interval != "" {
		s.Config.PollInterval = interval
	}
	if grace != 3 && grace >= 0 {
		s.Config.PollGrace = grace
	}

	if err := s.saveAndNotify(); err != nil {
		return err
	}

	return nil
}

// Returns WakaTime enabled/disabled status for user
func (s *CLIService) StatusWakatime() error {
	if s.Config.WakaTime.Enabled {
		fmt.Println("enabled")
	} else {
		fmt.Println("disabled")
	}

	return nil
}

// Returns Wakapi enabled/disabled status for user
func (s *CLIService) StatusWakapi() error {
	if s.Config.Wakapi.Enabled {
		fmt.Println("enabled")
	} else {
		fmt.Println("disabled")
	}

	return nil
}

// Display comprehensive statistics about the system
func (s *CLIService) GetStats(ctx context.Context) error {
	// Define color styles
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FAFAFA")).
		Background(lipgloss.Color("#7D56F4"))

	sectionTitleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FF9500"))

	programNameStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#38B6FF"))

	categoryStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFD700"))

	projectStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FF6B9D"))

	lifetimeStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#00FF88"))

	recentSessionsStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#A78BFA"))

	sessionTimeStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("#808080"))

	sessionDurationStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FFFFFF"))

	enabledStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#00FF00"))

	disabledStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("#FF0000"))

	// Title
	fmt.Println(titleStyle.Render("TIMEKEEP STATISTICS REPORT"))
	fmt.Println()

	// Service Status
	fmt.Println(sectionTitleStyle.Render("🔌 SERVICE STATUS"))
	if err := s.getServiceStatusString(nil); err != nil {
		fmt.Printf("  ⚠️  %v\n", err)
	}
	fmt.Println()

	// Active Sessions
	fmt.Println(sectionTitleStyle.Render("🔄 ACTIVE SESSIONS"))
	activeSessions, err := s.AsRepo.GetAllActiveSessions(ctx)
	if err != nil {
		fmt.Printf("  Error getting active sessions: %v\n", err)
	} else if len(activeSessions) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, session := range activeSessions {
			duration := time.Since(session.StartTime)
			fmt.Printf("  • %s - ", programNameStyle.Render(session.ProgramName))
			s.formatDurationToString(nil, duration)
		}
	}
	fmt.Println()

	// Tracked Programs
	fmt.Println(sectionTitleStyle.Render("📋 TRACKED PROGRAMS"))
	programs, err := s.PrRepo.GetAllPrograms(ctx)
	if err != nil {
		fmt.Printf("  Error getting programs: %v\n", err)
	} else if len(programs) == 0 {
		fmt.Println("  (none)")
	} else {
		for _, program := range programs {
			duration := time.Duration(program.LifetimeSeconds) * time.Second
			fmt.Printf("  └─ %s\n", programNameStyle.Render(program.Name))

			// Category
			if program.Category.Valid && program.Category.String != "" {
				fmt.Printf("      └─ %s: %s\n", categoryStyle.Render("Category"), program.Category.String)
			}

			// Project
			if program.Project.Valid && program.Project.String != "" {
				fmt.Printf("      └─ %s: %s\n", projectStyle.Render("Project"), program.Project.String)
			}

			// Lifetime info
			fmt.Print("      └─ ")
			fmt.Print(lifetimeStyle.Render("Lifetime"))
			fmt.Print(": ")
			if duration < time.Minute {
				fmt.Printf("%d seconds\n", int(duration.Seconds()))
			} else if duration < time.Hour {
				fmt.Printf("%d minutes\n", int(duration.Minutes()))
			} else {
				hours := int(duration.Hours())
				minutes := int(duration.Minutes()) % 60
				fmt.Printf("%dh %dm\n", hours, minutes)
			}

			// Get recent history for this program
			history, err := s.HsRepo.GetSessionHistory(ctx, database.GetSessionHistoryParams{
				ProgramName: program.Name,
				Limit:       3,
			})
			if err == nil && len(history) > 0 {
				fmt.Print("      └─ ")
				fmt.Println(recentSessionsStyle.Render("Recent Sessions"))
				for j, session := range history {
					isLastHistory := j == len(history)-1
					historyPrefix := "          ├─ "
					if isLastHistory {
						historyPrefix = "          └─ "
					}

					sessionDuration := time.Duration(session.DurationSeconds) * time.Second
					fmt.Printf("%s%s - %s ", historyPrefix,
						sessionTimeStyle.Render(session.StartTime.Format("2006-01-02 15:04")),
						sessionTimeStyle.Render(session.EndTime.Format("15:04")))

					if sessionDuration < time.Minute {
						fmt.Printf("%s\n", sessionDurationStyle.Render(fmt.Sprintf("(%d seconds)", int(sessionDuration.Seconds()))))
					} else if sessionDuration < time.Hour {
						fmt.Printf("%s\n", sessionDurationStyle.Render(fmt.Sprintf("(%d minutes)", int(sessionDuration.Minutes()))))
					} else {
						hours := int(sessionDuration.Hours())
						minutes := int(sessionDuration.Minutes()) % 60
						fmt.Printf("%s\n", sessionDurationStyle.Render(fmt.Sprintf("(%dh %dm)", hours, minutes)))
					}
				}
			}
		}
	}
	fmt.Println()

	// WakaTime Status
	fmt.Println(sectionTitleStyle.Render("⏱️  WAKATIME INTEGRATION"))
	if s.Config.WakaTime.Enabled {
		fmt.Printf("  Status: %s\n", enabledStyle.Render("ENABLED"))
		if s.Config.WakaTime.CLIPath != "" {
			fmt.Printf("  CLI Path: %s\n", s.Config.WakaTime.CLIPath)
		}
		if s.Config.WakaTime.GlobalProject != "" {
			fmt.Printf("  Global Project: %s\n", s.Config.WakaTime.GlobalProject)
		}
	} else {
		fmt.Printf("  Status: %s\n", disabledStyle.Render("DISABLED"))
	}
	fmt.Println()

	// Wakapi Status
	fmt.Println(sectionTitleStyle.Render("🌐 WAKAPI INTEGRATION"))
	if s.Config.Wakapi.Enabled {
		fmt.Printf("  Status: %s\n", enabledStyle.Render("ENABLED"))
		if s.Config.Wakapi.Server != "" {
			fmt.Printf("  Server: %s\n", s.Config.Wakapi.Server)
		}
		if s.Config.Wakapi.GlobalProject != "" {
			fmt.Printf("  Global Project: %s\n", s.Config.Wakapi.GlobalProject)
		}
	} else {
		fmt.Printf("  Status: %s\n", disabledStyle.Render("DISABLED"))
	}
	fmt.Println()

	return nil
}

// Helper function to get service status as string instead of printing
func (s *CLIService) getServiceStatusString(sb *strings.Builder) error {
	status, err := s.GetServiceStatusString()
	if err != nil {
		return err
	}
	if sb != nil {
		sb.WriteString(fmt.Sprintf("  Status: %s\n", status))
	} else {
		fmt.Printf("  Status: %s\n", status)
	}
	return nil
}

// Helper function to format duration to string
func (s *CLIService) formatDurationToString(sb *strings.Builder, duration time.Duration) {
	if duration < time.Minute {
		if sb != nil {
			sb.WriteString(fmt.Sprintf("%d seconds\n", int(duration.Seconds())))
		} else {
			fmt.Printf("%d seconds\n", int(duration.Seconds()))
		}
	} else if duration < time.Hour {
		if sb != nil {
			sb.WriteString(fmt.Sprintf("%d minutes\n", int(duration.Minutes())))
		} else {
			fmt.Printf("%d minutes\n", int(duration.Minutes()))
		}
	} else {
		hours := int(duration.Hours())
		minutes := int(duration.Minutes()) % 60
		if sb != nil {
			sb.WriteString(fmt.Sprintf("%dh %dm\n", hours, minutes))
		} else {
			fmt.Printf("%dh %dm\n", hours, minutes)
		}
	}
}
