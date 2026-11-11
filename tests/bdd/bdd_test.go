//go:build bdd

package bdd

import (
	"context"
	"testing"

	"github.com/cucumber/godog"
)

func initializeScenario(sc *godog.ScenarioContext) {
	s := &scenario{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		*s = scenario{c: newClient()}
		return ctx, nil
	})

	sc.Step(`^um cliente com e-mail e um veículo cadastrados$`, s.customerAndVehicle)
	sc.Step(`^dois serviços no catálogo$`, s.twoCatalogServices)
	sc.Step(`^a bancada da filial "([^"]*)" está disponível$`, s.bayAvailability(true))
	sc.Step(`^a bancada da filial "([^"]*)" fica indisponível$`, s.bayAvailability(false))
	sc.Step(`^o operador abre uma ordem de serviço$`, s.openWorkOrder)
	sc.Step(`^o mecânico conclui o diagnóstico incluindo os dois serviços$`, s.diagnoseWithBothServices)
	sc.Step(`^o cliente aprova o orçamento inteiro$`, s.decideWholeQuote("approve-all"))
	sc.Step(`^o cliente reprova o orçamento inteiro$`, s.decideWholeQuote("reject-all"))
	sc.Step(`^o Mercado Pago confirma o pagamento$`, s.payment("approved"))
	sc.Step(`^o Mercado Pago recusa o pagamento$`, s.payment("rejected"))
	sc.Step(`^o mecânico executa todos os serviços$`, s.executeAllServices)
	sc.Step(`^a OS fica "([^"]*)"$`, s.workOrderStatus)
	sc.Step(`^a saga fica "([^"]*)"$`, s.sagaStatus)
	sc.Step(`^a ficha da bancada fica "([^"]*)"$`, s.ticketStatus)
	sc.Step(`^o orçamento fica "([^"]*)"$`, s.quoteStatus)
	sc.Step(`^o pagamento fica "([^"]*)"$`, s.paymentStatus)
	sc.Step(`^o histórico registra "([^"]*)"$`, s.historyIs)

	sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
		if s.c != nil && s.c.token != "" {
			_ = s.bayAvailability(true)(ctx, "MATRIZ")
		}
		return ctx, nil
	})
}

func TestFeatures(t *testing.T) {
	suite := godog.TestSuite{
		ScenarioInitializer: initializeScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"features"},
			Strict:   true,
			TestingT: t,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("cenarios BDD falharam")
	}
}
