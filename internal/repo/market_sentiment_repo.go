package repo

import (
	"context"
	"time"

	"gorm.io/gorm"

	"stock-backend/internal/model"
)

type MarketSentimentRepo interface {
	// Upsert 创建或更新当日市场情绪数据
	Upsert(ctx context.Context, m *model.MarketSentiment) error
	// GetLatest 获取最新一条市场情绪数据
	GetLatest(ctx context.Context) (*model.MarketSentiment, error)
	// GetByDate 获取指定日期的市场情绪数据
	GetByDate(ctx context.Context, date time.Time) (*model.MarketSentiment, error)
}

type gormMarketSentimentRepo struct {
	db *gorm.DB
}

func NewMarketSentimentRepo(db *gorm.DB) MarketSentimentRepo {
	return &gormMarketSentimentRepo{db: db}
}

func (r *gormMarketSentimentRepo) Upsert(ctx context.Context, m *model.MarketSentiment) error {
	// 使用 trade_date 作为唯一键，进行 Upsert 操作
	return r.db.WithContext(ctx).
		Where(model.MarketSentiment{TradeDate: m.TradeDate}).
		Assign(m).
		FirstOrCreate(m).Error
}

func (r *gormMarketSentimentRepo) GetLatest(ctx context.Context) (*model.MarketSentiment, error) {
	var m model.MarketSentiment
	err := r.db.WithContext(ctx).
		Order("trade_date DESC").
		First(&m).Error
	if err != nil {
		return nil, err
	}
	return &m, nil
}

func (r *gormMarketSentimentRepo) GetByDate(ctx context.Context, date time.Time) (*model.MarketSentiment, error) {
	var m model.MarketSentiment
	err := r.db.WithContext(ctx).
		Where("trade_date = ?", date).
		First(&m).Error
	if err != nil {
		return nil, err
	}
	return &m, nil
}
