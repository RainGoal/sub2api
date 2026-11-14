package service

import "github.com/gin-gonic/gin"

const openAICollaborationBudgetContextKey = "openai_collaboration_request_budget"

// This budget belongs to one logical request/WS turn, even when account or
// group failover leaves the configured collaboration scope temporarily.
type openAICollaborationRequestBudget struct {
	upstreamAttempts int
	recoveryUsed     bool
	stopFailover     bool
}

func collaborationRequestBudget(c *gin.Context) *openAICollaborationRequestBudget {
	if c == nil {
		return nil
	}
	value, _ := c.Get(openAICollaborationBudgetContextKey)
	budget, _ := value.(*openAICollaborationRequestBudget)
	return budget
}

func noteCollaborationUpstreamAttempt(c *gin.Context) {
	if c == nil {
		return
	}
	budget := collaborationRequestBudget(c)
	if budget == nil {
		budget = &openAICollaborationRequestBudget{}
		c.Set(openAICollaborationBudgetContextKey, budget)
	}
	budget.upstreamAttempts++
	if attempt := collaborationAttempt(c); attempt != nil {
		attempt.upstreamAttempts = budget.upstreamAttempts
	}
}

func collaborationPriorUpstreamAttempts(c *gin.Context) int {
	if budget := collaborationRequestBudget(c); budget != nil {
		return budget.upstreamAttempts
	}
	return 0
}
