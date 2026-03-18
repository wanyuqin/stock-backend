package service

import (
	"context"
	"encoding/json"
	"fmt"

	"go.uber.org/zap"

	"stock-backend/internal/model"
	"stock-backend/internal/repo"
)

// TemplateParams 筛选模板参数（与 ScreenerRequest 对齐）
type TemplateParams struct {
	MinScore           int     `json:"min_score"`
	MinMainInflowPct   float64 `json:"min_main_inflow_pct"`
	RequireBullAligned bool    `json:"require_bull_aligned"`
	MinVolRatio        float64 `json:"min_vol_ratio"`
}

type CreateTemplateRequest struct {
	Name        string         `json:"name"         binding:"required"`
	Description string         `json:"description"`
	Params      TemplateParams `json:"params"`
	PushEnabled bool           `json:"push_enabled"`
}

type UpdateTemplateRequest struct {
	Name        *string         `json:"name"`
	Description *string         `json:"description"`
	Params      *TemplateParams `json:"params"`
	PushEnabled *bool           `json:"push_enabled"`
}

type ScreenerTemplateService struct {
	repo        repo.ScreenerTemplateRepo
	screenerSvc *ScreenerService
	log         *zap.Logger
}

func NewScreenerTemplateService(
	r repo.ScreenerTemplateRepo,
	screenerSvc *ScreenerService,
	log *zap.Logger,
) *ScreenerTemplateService {
	return &ScreenerTemplateService{repo: r, screenerSvc: screenerSvc, log: log}
}

func (s *ScreenerTemplateService) List(ctx context.Context, userID int64) ([]*model.ScreenerTemplate, error) {
	return s.repo.ListByUser(ctx, userID)
}

func (s *ScreenerTemplateService) Create(ctx context.Context, userID int64, req *CreateTemplateRequest) (*model.ScreenerTemplate, error) {
	paramsJSON, err := json.Marshal(req.Params)
	if err != nil {
		return nil, fmt.Errorf("序列化参数失败: %w", err)
	}
	t := &model.ScreenerTemplate{
		UserID:      userID,
		Name:        req.Name,
		Description: req.Description,
		Params:      paramsJSON,
		PushEnabled: req.PushEnabled,
	}
	if err := s.repo.Create(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *ScreenerTemplateService) Update(ctx context.Context, userID, id int64, req *UpdateTemplateRequest) (*model.ScreenerTemplate, error) {
	t, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("模板不存在")
	}
	if t.UserID != userID {
		return nil, fmt.Errorf("无权限")
	}
	if req.Name != nil        { t.Name = *req.Name }
	if req.Description != nil { t.Description = *req.Description }
	if req.PushEnabled != nil { t.PushEnabled = *req.PushEnabled }
	if req.Params != nil {
		b, _ := json.Marshal(req.Params)
		t.Params = b
	}
	if err := s.repo.Update(ctx, t); err != nil {
		return nil, err
	}
	return t, nil
}

func (s *ScreenerTemplateService) Delete(ctx context.Context, userID, id int64) error {
	t, err := s.repo.GetByID(ctx, id)
	if err != nil {
		return fmt.Errorf("模板不存在")
	}
	if t.UserID != userID {
		return fmt.Errorf("无权限")
	}
	return s.repo.Delete(ctx, id)
}

// RunAllPushTemplates 供 cron 调用，每日 16:00 跑所有 push_enabled=true 的模板
func (s *ScreenerTemplateService) RunAllPushTemplates(ctx context.Context, userID int64) error {
	templates, err := s.repo.ListByUser(ctx, userID)
	if err != nil {
		return err
	}
	for _, t := range templates {
		if !t.PushEnabled {
			continue
		}
		var params TemplateParams
		if err := json.Unmarshal(t.Params, &params); err != nil {
			s.log.Warn("screener template: unmarshal params failed", zap.Int64("id", t.ID), zap.Error(err))
			continue
		}
		result, err := s.screenerSvc.Execute(ctx, ScreenerRequest{
			MinScore: params.MinScore,
			Limit:    20,
		})
		if err != nil {
			s.log.Error("screener template: execute failed", zap.Int64("id", t.ID), zap.Error(err))
			continue
		}
		s.log.Info("screener template: executed",
			zap.String("name", t.Name),
			zap.Int("matched", result.Matched),
			zap.Int("returned", len(result.Items)),
		)
		// TODO: 接入企业微信推送后在此处发送通知
	}
	return nil
}
