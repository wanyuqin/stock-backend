package service

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"

	"stock-backend/internal/model"
)

// UpdateStatus 更新计划状态。
// 若 status == "EXECUTED" 且传入了 tradeLogID，则同时写入关联交易记录。
// 若系统中已有对应持仓（PositionGuardianService 可用），自动继承止损/目标/理由。
func (s *BuyPlanService) UpdateStatus(ctx context.Context, userID, id int64, status string, tradeLogID *int64) error {
	plan, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("计划不存在")
	}
	if plan.UserID != userID {
		return fmt.Errorf("无权限操作此计划")
	}

	newStatus := model.BuyPlanStatus(status)

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

		// 异步把计划的止损/目标/理由写入持仓守护
		if s.guardianSvc != nil {
			go func() {
				bgCtx := context.Background()
				var stopLoss, targetPrice *float64
				if plan.StopLossPrice != nil {
					v := *plan.StopLossPrice
					stopLoss = &v
				}
				if plan.TargetPrice != nil {
					v := *plan.TargetPrice
					targetPrice = &v
				}
				if linkErr := s.guardianSvc.LinkPlanToPosition(
					bgCtx,
					plan.StockCode,
					plan.ID,
					stopLoss,
					targetPrice,
					plan.Reason,
				); linkErr != nil {
					s.log.Warn("LinkPlanToPosition failed",
						zap.String("code", plan.StockCode),
						zap.Error(linkErr),
					)
				}
			}()
		}
		return nil
	}

	return s.repo.UpdateStatus(ctx, id, newStatus)
}
