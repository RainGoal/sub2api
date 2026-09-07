package service

import "context"

type affiliateSalesCustomerReader interface {
	IsSalesCustomer(context.Context, int64) (bool, error)
}

func (s *AffiliateService) isSalesCustomer(ctx context.Context, userID int64) (bool, error) {
	if reader, ok := s.repo.(affiliateSalesCustomerReader); ok {
		return reader.IsSalesCustomer(ctx, userID)
	}
	return false, nil
}
