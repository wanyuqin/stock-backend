package repo

import (
	"context"
	"errors"
	"fmt"

	"gorm.io/gorm"

	"stock-backend/internal/model"
)

// ═══════════════════════════════════════════════════════════════
// StockRepo — GORM 实现
// ═══════════════════════════════════════════════════════════════

type stockRepo struct{ db *gorm.DB }

func NewStockRepo(db *gorm.DB) StockRepo { return &stockRepo{db: db} }

func (r *stockRepo) GetByCode(ctx context.Context, code string) (*model.Stock, error) {
	var s model.Stock
	err := r.db.WithContext(ctx).Where("code = ?", code).First(&s).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, fmt.Errorf("stock %q not found", code)
		}
		return nil, fmt.Errorf("GetByCode: %w", err)
	}
	return &s, nil
}

func (r *stockRepo) List(ctx context.Context, limit, offset int) ([]*model.Stock, error) {
	var stocks []*model.Stock
	err := r.db.WithContext(ctx).
		Order("code ASC").Limit(limit).Offset(offset).
		Find(&stocks).Error
	if err != nil {
		return nil, fmt.Errorf("List stocks: %w", err)
	}
	return stocks, nil
}

func (r *stockRepo) Upsert(ctx context.Context, s *model.Stock) error {
	err := r.db.WithContext(ctx).
		Where(model.Stock{Code: s.Code}).
		Assign(model.Stock{Name: s.Name, Market: s.Market, Sector: s.Sector}).
		FirstOrCreate(s).Error
	if err != nil {
		return fmt.Errorf("Upsert stock: %w", err)
	}
	return nil
}

// ═══════════════════════════════════════════════════════════════
// WatchlistRepo — GORM 实现
// ═══════════════════════════════════════════════════════════════

type watchlistRepo struct{ db *gorm.DB }

func NewWatchlistRepo(db *gorm.DB) WatchlistRepo { return &watchlistRepo{db: db} }

func (r *watchlistRepo) ListByUser(ctx context.Context, userID int64) ([]*model.Watchlist, error) {
	var items []*model.Watchlist
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).Order("created_at DESC").Find(&items).Error
	if err != nil {
		return nil, fmt.Errorf("ListByUser watchlist: %w", err)
	}
	return items, nil
}

func (r *watchlistRepo) Add(ctx context.Context, w *model.Watchlist) error {
	err := r.db.WithContext(ctx).
		Where(model.Watchlist{UserID: w.UserID, StockCode: w.StockCode}).
		FirstOrCreate(w).Error
	if err != nil {
		return fmt.Errorf("Add watchlist: %w", err)
	}
	return nil
}

func (r *watchlistRepo) Remove(ctx context.Context, userID int64, stockCode string) error {
	result := r.db.WithContext(ctx).
		Where("user_id = ? AND stock_code = ?", userID, stockCode).
		Delete(&model.Watchlist{})
	if result.Error != nil {
		return fmt.Errorf("Remove watchlist: %w", result.Error)
	}
	if result.RowsAffected == 0 {
		return fmt.Errorf("watchlist entry %q not found for user %d", stockCode, userID)
	}
	return nil
}

// ═══════════════════════════════════════════════════════════════
// TradeLogRepo — GORM 实现
// ═══════════════════════════════════════════════════════════════

type tradeLogRepo struct{ db *gorm.DB }

func NewTradeLogRepo(db *gorm.DB) TradeLogRepo { return &tradeLogRepo{db: db} }

// Create 插入一条交易记录，使用 Session 禁止 GORM 推断主键（让 DB 自增）。
func (r *tradeLogRepo) Create(ctx context.Context, t *model.TradeLog) error {
	if err := r.db.WithContext(ctx).Create(t).Error; err != nil {
		return fmt.Errorf("Create trade_log: %w", err)
	}
	return nil
}

// ListByUser 分页查询某用户所有交易记录，traded_at 倒序。
func (r *tradeLogRepo) ListByUser(ctx context.Context, userID int64, limit, offset int) ([]*model.TradeLog, error) {
	var logs []*model.TradeLog
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("traded_at DESC, id DESC").
		Limit(limit).Offset(offset).
		Find(&logs).Error
	if err != nil {
		return nil, fmt.Errorf("ListByUser trade_logs: %w", err)
	}
	return logs, nil
}

// ListByCode 查询某只股票的全部交易记录，traded_at 倒序（不分页）。
func (r *tradeLogRepo) ListByCode(ctx context.Context, userID int64, code string) ([]*model.TradeLog, error) {
	var logs []*model.TradeLog
	err := r.db.WithContext(ctx).
		Where("user_id = ? AND stock_code = ?", userID, code).
		Order("traded_at DESC, id DESC").
		Find(&logs).Error
	if err != nil {
		return nil, fmt.Errorf("ListByCode trade_logs: %w", err)
	}
	return logs, nil
}

// ListAllByUser 不分页查询全部交易记录，供盈亏计算使用。
func (r *tradeLogRepo) ListAllByUser(ctx context.Context, userID int64) ([]*model.TradeLog, error) {
	var logs []*model.TradeLog
	err := r.db.WithContext(ctx).
		Where("user_id = ?", userID).
		Order("stock_code ASC, traded_at ASC, id ASC"). // 按股票分组后按时间升序，便于 FIFO 计算
		Find(&logs).Error
	if err != nil {
		return nil, fmt.Errorf("ListAllByUser trade_logs: %w", err)
	}
	return logs, nil
}
