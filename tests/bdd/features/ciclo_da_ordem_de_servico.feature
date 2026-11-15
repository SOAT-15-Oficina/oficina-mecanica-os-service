# language: pt
Funcionalidade: Ciclo de vida da ordem de serviço atravessando os três microsserviços
  Para atender clientes em várias filiais
  Como oficina mecânica
  Quero que a OS passe por diagnóstico, orçamento, pagamento e execução
  E que qualquer falha antes do reparo seja desfeita automaticamente

  Contexto:
    Dado um cliente com e-mail e um veículo cadastrados
    E dois serviços no catálogo
    E a bancada da filial "MATRIZ" está disponível

  Cenário: OS paga é executada até o fim
    Quando o operador abre uma ordem de serviço
    E o mecânico conclui o diagnóstico incluindo os dois serviços
    Então a OS fica "AGUARDANDO_APROVACAO"
    Quando o cliente aprova o orçamento inteiro
    Então a OS fica "AGUARDANDO_PAGAMENTO"
    Quando o Mercado Pago confirma o pagamento
    Então a OS fica "APROVADO"
    Quando o mecânico executa todos os serviços
    Então a OS fica "FINALIZADA"
    E o histórico registra "RECEBIDA, EM_DIAGNOSTICO, AGUARDANDO_APROVACAO, AGUARDANDO_PAGAMENTO, APROVADO, EM_EXECUCAO, FINALIZADA"
    E a saga fica "COMPLETED"

  Cenário: cliente reprova o orçamento
    Quando o operador abre uma ordem de serviço
    E o mecânico conclui o diagnóstico incluindo os dois serviços
    E o cliente reprova o orçamento inteiro
    Então a OS fica "CANCELADA"
    E a saga fica "COMPENSATED"
    E a ficha da bancada fica "CANCELADO"

  Cenário: QR Code do pagamento expira sem ser pago
    Quando o operador abre uma ordem de serviço
    E o mecânico conclui o diagnóstico incluindo os dois serviços
    E o cliente aprova o orçamento inteiro
    E o QR Code do pagamento expira
    Então a OS fica "CANCELADA"
    E a saga fica "COMPENSATED"
    E o orçamento fica "CANCELADO"

  Cenário: bancada recusa o reparo depois do pagamento e o pagamento é estornado
    Quando o operador abre uma ordem de serviço
    E o mecânico conclui o diagnóstico incluindo os dois serviços
    E o cliente aprova o orçamento inteiro
    E a bancada da filial "MATRIZ" fica indisponível
    E o Mercado Pago confirma o pagamento
    Então a OS fica "CANCELADA"
    E a saga fica "COMPENSATED"
    E o pagamento fica "ESTORNADO"
    E a ficha da bancada fica "CANCELADO"
