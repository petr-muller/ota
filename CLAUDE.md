# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

This is the OTA (OpenShift Test Automation) repository, a Go-based tool suite for monitoring and managing OpenShift upgrade-blocking Jira issues. The project consists of multiple command-line tools that help track and manage the lifecycle of upgrade blocker issues in Red Hat's Jira instance.

## Commands

### Building
```bash
go build ./cmd/monitor              # Build the interactive TUI monitor
go build ./cmd/monitor-jira-dashboard/   # Build the dashboard tool
```

### Running Tools
```bash
# Interactive TUI for monitoring upgrade blockers
./monitor -jira.bearer-token-file=~/.config/ota/jira-token

# Dashboard view of upgrade blocker status
./monitor-jira-dashboard -jira.bearer-token-file=~/.config/ota/jira-token

# Automated label management tools
./monitor-jira-clear-labels
./monitor-jira-create-impact-statement-request
./monitor-jira-move-to-proposed
./monitor-jira-move-to-updaterecommendationblocked
```

### Development
```bash
go mod tidy                         # Update dependencies
go fmt ./...                        # Format code
go vet ./...                        # Static analysis
```

## Architecture

### Core Components

- **cmd/**: Contains multiple CLI tools, each serving a specific purpose in the upgrade blocker workflow
- **internal/config/**: Configuration management, primarily for determining user config directory paths
- **internal/flagutil/**: Jira authentication and connection utilities built on top of Prow's flagutil
- **internal/updateblockers/**: Constants and utilities for managing Jira labels used in the upgrade blocker process

### Jira Integration

The tools integrate with Red Hat's Jira instance (https://issues.redhat.com) using bearer token authentication. The main workflow involves:

1. **UpgradeBlocker** issues that need impact statement requests
2. **ImpactStatementRequested** issues waiting for developer input  
3. **ImpactStatementProposed** issues with developer-provided statements
4. **UpdateRecommendationsBlocked** issues that are known and documented

### Key Technologies

- **Bubble Tea**: Terminal UI framework for the interactive monitor
- **Prow**: Kubernetes testing infrastructure (used for Jira client utilities)
- **go-jira**: Jira API client library
- **logrus**: Structured logging

## Configuration

Authentication requires a Jira bearer token stored at `~/.config/ota/jira-token` by default. The configuration directory path is determined by `internal/config/dir.go`.

## Tools Purpose

- **monitor**: Interactive TUI for browsing upgrade blocker issues with table view and browser integration
- **monitor-jira-dashboard**: Static dashboard showing current state across all upgrade blocker categories
- **graph-** tools: Utilities for managing dependency graphs (likely OpenShift update graphs)
- **monitor-jira-** tools: Automated label management for moving issues through the upgrade blocker workflow

## Prompts Directory

The `prompts/` directory contains prompts to kick off "larger" tasks with Claude. It should be used instead of writing a giant prompt that contains all the instructions for the task. Inclusion in the repository is also helpful to provide context about how a certain thing was built or how it works later, for both humans and AI.