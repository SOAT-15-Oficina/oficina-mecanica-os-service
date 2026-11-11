//go:build bdd

package bdd

import (
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strings"
	"time"
)

const waitFor = 45 * time.Second

type scenario struct {
	c           *client
	customerID  string
	vehicleID   string
	serviceIDs  []string
	workOrderID string
	itemIDs     []string
}

type quote struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	Payments []struct {
		Status string `json:"status"`
	} `json:"payments"`
}

func (s *scenario) customerAndVehicle(ctx context.Context) error {
	if err := s.c.login(ctx); err != nil {
		return err
	}

	var customer struct {
		ID string `json:"id"`
	}
	if err := s.c.call(ctx, http.MethodPost, "/customers", map[string]string{
		"name":          "Cliente BDD",
		"email":         "cliente.bdd@workshop.local",
		"document":      randomCPF(),
		"document_type": "CPF",
	}, &customer); err != nil {
		return err
	}
	s.customerID = customer.ID

	var vehicle struct {
		ID string `json:"id"`
	}
	if err := s.c.call(ctx, http.MethodPost, "/vehicles", map[string]any{
		"license_plate": randomPlate(),
		"customer_id":   s.customerID,
		"brand":         "Chevrolet",
		"model":         "Onix",
		"year":          2021,
	}, &vehicle); err != nil {
		return err
	}
	s.vehicleID = vehicle.ID
	return nil
}

func (s *scenario) twoCatalogServices(ctx context.Context) error {
	for _, title := range []string{"Troca de oleo", "Alinhamento"} {
		var svc struct {
			ID string `json:"id"`
		}
		if err := s.c.call(ctx, http.MethodPost, "/services", map[string]any{
			"title":                  fmt.Sprintf("%s BDD %d", title, rand.IntN(1_000_000_000)),
			"description":            "servico criado pelo BDD",
			"price_cents":            12000,
			"estimated_time_minutes": 30,
			"active":                 true,
		}, &svc); err != nil {
			return err
		}
		s.serviceIDs = append(s.serviceIDs, svc.ID)
	}
	return nil
}

func (s *scenario) bayAvailability(available bool) func(context.Context, string) error {
	return func(ctx context.Context, branch string) error {
		return s.c.call(ctx, http.MethodPut, "/execution/bays/"+branch+"/availability", map[string]bool{"available": available}, nil)
	}
}

func (s *scenario) openWorkOrder(ctx context.Context) error {
	var wo struct {
		ID string `json:"id"`
	}
	if err := s.c.call(ctx, http.MethodPost, "/work-orders", map[string]string{
		"title":       "Revisao BDD",
		"customer_id": s.customerID,
		"vehicle_id":  s.vehicleID,
	}, &wo); err != nil {
		return err
	}
	s.workOrderID = wo.ID
	return s.ticketStatus(ctx, "DIAGNOSTICO_NA_FILA")
}

func (s *scenario) diagnoseWithBothServices(ctx context.Context) error {
	base := "/execution/tickets/" + s.workOrderID
	if err := s.c.call(ctx, http.MethodPost, base+"/diagnosis/start", map[string]string{"technician": "Mecanico BDD"}, nil); err != nil {
		return err
	}
	if err := s.workOrderStatus(ctx, "EM_DIAGNOSTICO"); err != nil {
		return err
	}

	body := make([]map[string]string, 0, len(s.serviceIDs))
	for _, id := range s.serviceIDs {
		body = append(body, map[string]string{"service_id": id})
	}
	var items []struct {
		ID string `json:"id"`
	}
	if err := s.c.call(ctx, http.MethodPost, "/work-orders/"+s.workOrderID+"/services", body, &items); err != nil {
		return err
	}
	for _, it := range items {
		s.itemIDs = append(s.itemIDs, it.ID)
	}

	return s.c.call(ctx, http.MethodPost, base+"/diagnosis/complete", map[string]string{"notes": "diagnostico BDD"}, nil)
}

func (s *scenario) currentQuote(ctx context.Context) (quote, error) {
	var q quote
	err := s.c.call(ctx, http.MethodGet, "/billing/quotes/by-work-order/"+s.workOrderID, nil, &q)
	return q, err
}

func (s *scenario) decideWholeQuote(decision string) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := s.workOrderStatus(ctx, "AGUARDANDO_APROVACAO"); err != nil {
			return err
		}
		q, err := s.currentQuote(ctx)
		if err != nil {
			return err
		}
		return s.c.call(ctx, http.MethodGet, "/billing/public/quotes/"+q.ID+"/"+decision, nil, nil)
	}
}

func (s *scenario) payment(status string) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := s.workOrderStatus(ctx, "AGUARDANDO_PAGAMENTO"); err != nil {
			return err
		}
		var wo struct {
			PaymentURL string `json:"payment_url"`
		}
		if err := eventually(ctx, waitFor, func() (bool, string, error) {
			err := s.c.call(ctx, http.MethodGet, "/work-orders/"+s.workOrderID, nil, &wo)
			return wo.PaymentURL != "", wo.PaymentURL, err
		}); err != nil {
			return err
		}
		preference := wo.PaymentURL[strings.LastIndex(wo.PaymentURL, "/")+1:]
		_, err := s.c.do(ctx, http.MethodPost, s.c.mpfake+"/checkout/"+preference+"/pay?status="+status, nil, nil)
		return err
	}
}

func (s *scenario) executeAllServices(ctx context.Context) error {
	if err := s.ticketStatus(ctx, "REPARO_NA_FILA"); err != nil {
		return err
	}
	base := "/execution/tickets/" + s.workOrderID
	if err := s.c.call(ctx, http.MethodPost, base+"/repair/start", map[string]string{"technician": "Mecanico BDD"}, nil); err != nil {
		return err
	}
	for _, id := range s.itemIDs {
		if err := s.c.call(ctx, http.MethodPost, base+"/items/"+id+"/start", nil, nil); err != nil {
			return err
		}
		if err := s.c.call(ctx, http.MethodPost, base+"/items/"+id+"/finish", nil, nil); err != nil {
			return err
		}
	}
	return nil
}

func (s *scenario) workOrderStatus(ctx context.Context, want string) error {
	return eventually(ctx, waitFor, func() (bool, string, error) {
		var wo struct {
			Status string `json:"status"`
		}
		err := s.c.call(ctx, http.MethodGet, "/work-orders/"+s.workOrderID, nil, &wo)
		return wo.Status == want, wo.Status, err
	})
}

func (s *scenario) sagaStatus(ctx context.Context, want string) error {
	return eventually(ctx, waitFor, func() (bool, string, error) {
		var saga struct {
			Status string `json:"status"`
		}
		err := s.c.call(ctx, http.MethodGet, "/work-orders/"+s.workOrderID+"/saga", nil, &saga)
		return saga.Status == want, saga.Status, err
	})
}

func (s *scenario) ticketStatus(ctx context.Context, want string) error {
	return eventually(ctx, waitFor, func() (bool, string, error) {
		var ticket struct {
			Status string `json:"status"`
		}
		err := s.c.call(ctx, http.MethodGet, "/execution/tickets/"+s.workOrderID, nil, &ticket)
		return ticket.Status == want, ticket.Status, err
	})
}

func (s *scenario) quoteStatus(ctx context.Context, want string) error {
	return eventually(ctx, waitFor, func() (bool, string, error) {
		q, err := s.currentQuote(ctx)
		return q.Status == want, q.Status, err
	})
}

func (s *scenario) paymentStatus(ctx context.Context, want string) error {
	return eventually(ctx, waitFor, func() (bool, string, error) {
		q, err := s.currentQuote(ctx)
		if err != nil || len(q.Payments) == 0 {
			return false, "sem pagamento", err
		}
		got := q.Payments[len(q.Payments)-1].Status
		return got == want, got, nil
	})
}

func (s *scenario) historyIs(ctx context.Context, expected string) error {
	want := strings.Split(expected, ", ")
	return eventually(ctx, waitFor, func() (bool, string, error) {
		var history []struct {
			To string `json:"to"`
		}
		if err := s.c.call(ctx, http.MethodGet, "/work-orders/"+s.workOrderID+"/history", nil, &history); err != nil {
			return false, "", err
		}
		got := make([]string, 0, len(history))
		for _, h := range history {
			got = append(got, h.To)
		}
		return strings.Join(got, ", ") == strings.Join(want, ", "), strings.Join(got, ", "), nil
	})
}

func randomCPF() string {
	digits := make([]int, 9, 11)
	for i := range digits {
		digits[i] = rand.IntN(10)
	}
	for _, size := range []int{9, 10} {
		sum := 0
		for i := 0; i < size; i++ {
			sum += digits[i] * (size + 1 - i)
		}
		check := (sum * 10) % 11
		if check == 10 {
			check = 0
		}
		digits = append(digits, check)
	}
	var b strings.Builder
	for _, d := range digits {
		b.WriteByte(byte('0' + d))
	}
	return b.String()
}

func randomPlate() string {
	letters := "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	return fmt.Sprintf("%c%c%c%d%c%d%d",
		letters[rand.IntN(26)], letters[rand.IntN(26)], letters[rand.IntN(26)],
		rand.IntN(10), letters[rand.IntN(26)], rand.IntN(10), rand.IntN(10))
}
