-- +goose Up
-- +goose StatementBegin
ALTER TABLE "work_orders" ADD COLUMN "branch" varchar(40) NOT NULL DEFAULT 'MATRIZ';
ALTER TABLE "work_orders" ADD COLUMN "payment_url" text;
ALTER TABLE "work_orders" ADD COLUMN "approved_total_cents" int;

ALTER TABLE "work_orders" ADD CONSTRAINT work_orders_status_check CHECK ("status" IN (
  'RECEBIDA', 'EM_DIAGNOSTICO', 'AGUARDANDO_APROVACAO', 'AGUARDANDO_PAGAMENTO',
  'APROVADO', 'EM_EXECUCAO', 'FINALIZADA', 'ENTREGUE', 'CANCELADA'
));

CREATE TABLE "work_order_status_history" (
  "id" uuid PRIMARY KEY,
  "work_order_id" uuid NOT NULL REFERENCES "work_orders" ("id"),
  "from_status" varchar(30),
  "to_status" varchar(30) NOT NULL,
  "cause" varchar(80) NOT NULL,
  "message_id" uuid,
  "changed_at" timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_work_order_status_history_wo ON "work_order_status_history" ("work_order_id", "changed_at");

INSERT INTO "work_order_status_history" ("id", "work_order_id", "from_status", "to_status", "cause", "changed_at")
SELECT gen_random_uuid(), "id", NULL, "status", 'migration', "updated_at" FROM "work_orders";

CREATE TABLE "saga_instances" (
  "id" uuid PRIMARY KEY,
  "work_order_id" uuid NOT NULL UNIQUE REFERENCES "work_orders" ("id"),
  "status" varchar(20) NOT NULL CHECK ("status" IN ('STARTED', 'COMPENSATING', 'COMPLETED', 'COMPENSATED', 'FAILED')),
  "current_step" varchar(40) NOT NULL,
  "completed_steps" jsonb NOT NULL DEFAULT '[]'::jsonb,
  "pending_compensations" jsonb NOT NULL DEFAULT '[]'::jsonb,
  "deadline_at" timestamptz,
  "attempts" int NOT NULL DEFAULT 0,
  "last_command" jsonb,
  "quote_id" uuid,
  "failure_reason" varchar(60),
  "version" int NOT NULL DEFAULT 1,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX idx_saga_instances_deadline ON "saga_instances" ("deadline_at")
  WHERE "status" IN ('STARTED', 'COMPENSATING') AND "deadline_at" IS NOT NULL;

CREATE TABLE "outbox" (
  "id" uuid PRIMARY KEY,
  "destination" text NOT NULL,
  "message_type" text NOT NULL,
  "group_id" text NOT NULL,
  "body" jsonb NOT NULL,
  "trace_carrier" jsonb NOT NULL DEFAULT '{}'::jsonb,
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "published_at" timestamptz
);

CREATE INDEX outbox_pending ON "outbox" ("created_at") WHERE "published_at" IS NULL;

CREATE TABLE "processed_messages" (
  "message_id" uuid PRIMARY KEY,
  "message_type" text NOT NULL,
  "processed_at" timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE "supply_reservations" (
  "id" uuid PRIMARY KEY,
  "work_order_id" uuid NOT NULL REFERENCES "work_orders" ("id"),
  "work_order_service_id" uuid NOT NULL REFERENCES "work_order_services" ("id"),
  "supply_id" uuid NOT NULL REFERENCES "supplies" ("id"),
  "quantity" int NOT NULL CHECK ("quantity" > 0),
  "status" varchar(20) NOT NULL CHECK ("status" IN ('RESERVADA', 'LIBERADA', 'CONSUMIDA')),
  "created_at" timestamptz NOT NULL DEFAULT now(),
  "updated_at" timestamptz NOT NULL DEFAULT now(),
  UNIQUE ("work_order_service_id", "supply_id")
);

CREATE INDEX idx_supply_reservations_active ON "supply_reservations" ("supply_id") WHERE "status" = 'RESERVADA';
CREATE INDEX idx_supply_reservations_wo ON "supply_reservations" ("work_order_id");
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS "supply_reservations";
DROP TABLE IF EXISTS "processed_messages";
DROP TABLE IF EXISTS "outbox";
DROP TABLE IF EXISTS "saga_instances";
DROP TABLE IF EXISTS "work_order_status_history";
UPDATE "work_orders" SET "status" = 'AGUARDANDO_APROVACAO' WHERE "status" = 'AGUARDANDO_PAGAMENTO';
ALTER TABLE "work_orders" DROP CONSTRAINT IF EXISTS work_orders_status_check;
ALTER TABLE "work_orders" DROP COLUMN IF EXISTS "approved_total_cents";
ALTER TABLE "work_orders" DROP COLUMN IF EXISTS "payment_url";
ALTER TABLE "work_orders" DROP COLUMN IF EXISTS "branch";
-- +goose StatementEnd
