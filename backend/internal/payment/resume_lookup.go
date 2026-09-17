// 签名续接是唯一公开主动查单入口，普通公开查询不触发渠道请求。
package payment

import (
	"context"
	"fmt"
	"strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

func (s *OrderLifecycle) GetPublicOrderByResumeToken(ctx context.Context, token string) (*Order, error) {
	claims, err := s.resume.ParseToken(strings.TrimSpace(token))
	if err != nil {
		return nil, err
	}

	order, err := s.store.Order(ctx, claims.OrderID)
	if err != nil {
		if s.store.IsNotFound(err) {
			return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
		}
		return nil, fmt.Errorf("get order by resume token: %w", err)
	}
	if claims.UserID > 0 && order.UserID != claims.UserID {
		return nil, InvalidResumeTokenMatchError()
	}
	snapshot := PsOrderProviderSnapshot(order)
	orderProviderInstanceID := strings.TrimSpace(refundStringValue(order.ProviderInstanceID))
	orderProviderKey := strings.TrimSpace(refundStringValue(order.ProviderKey))
	if snapshot != nil {
		if snapshot.ProviderInstanceID != "" {
			orderProviderInstanceID = snapshot.ProviderInstanceID
		}
		if snapshot.ProviderKey != "" {
			orderProviderKey = snapshot.ProviderKey
		}
	}
	if claims.ProviderInstanceID != "" && orderProviderInstanceID != claims.ProviderInstanceID {
		return nil, InvalidResumeTokenMatchError()
	}
	if claims.ProviderKey != "" && !strings.EqualFold(orderProviderKey, claims.ProviderKey) {
		return nil, InvalidResumeTokenMatchError()
	}
	if claims.PaymentType != "" && NormalizeVisibleMethod(order.PaymentType) != NormalizeVisibleMethod(claims.PaymentType) {
		return nil, InvalidResumeTokenMatchError()
	}
	if order.Status == OrderStatusPending || order.Status == OrderStatusExpired {
		result, checkErr := s.CheckPaid(ctx, order)
		if checkErr != nil {
			return nil, checkErr
		}
		if result == LifecycleCheckPaidResultAlreadyPaid || result == LifecycleCheckPaidResultProcessing {
			order, err = s.store.Order(ctx, order.ID)
			if err != nil {
				return nil, fmt.Errorf("reload order by resume token: %w", err)
			}
		}
	}

	return order, nil
}
func InvalidResumeTokenMatchError() error {
	return infraerrors.BadRequest("INVALID_RESUME_TOKEN", "resume token does not match the payment order")
}
func (s *OrderLifecycle) ParseWeChatPaymentResumeToken(token string) (*WeChatPaymentResumeClaims, error) {
	return s.resume.ParseWeChatPaymentResumeToken(strings.TrimSpace(token))
}
