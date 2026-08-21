// Package backend defines the Run scheduler's internal execution port and
// historical snapshot adapters. It is not a public Driver or Workflow API.
// Production composition reaches concrete Agent Drivers through
// driver/scheduleradapter; new product contracts belong in package agent.
package backend
