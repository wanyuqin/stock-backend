package service

import (
	"context"
	"fmt"
	"time"

	"stock-backend/internal/model"
)

// UpdateStatus 更新计划状态。
// 若 status == "EXECUTED" 且传入了 tradeLogID，则同时写入关联交易记录，
// 形成「买入计划 → 执行交易」闭环。
func (s *BuyPlanService) UpdateStatus(ctx context.Context, userID, id int64, status string, tradeLogID *int64) error {
	plan, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("计划不存在")
	}
	if plan.UserID != userID {
		return fmt.Errorf("无权限操作此计划")
	}

	newStatus := model.BuyPlanStatus(status)

	// 标记执行时间 + 关联交易记录
	if newStatus == model.BuyPlanStatusExecuted {
		now := time.Now()
		plan.ExecutedAt = &now
		if tradeLogID != nil && *tradeLogID > 0 {
			plan.TradeLogID = tradeLogID
		}
		plan.Status = newStatus
		if err := s.repo.Update(ctx, plan); err != nil {
			return fmt.Errorf("更新计划失败: %w", err)
		}
		return nil
	}

	return s.repo.UpdateStatus(ctx, id, newStatus)
}
