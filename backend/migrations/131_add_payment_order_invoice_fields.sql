ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS payment_customer_id VARCHAR(128);
ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS payment_invoice_id VARCHAR(128);
ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS payment_invoice_url TEXT;
ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS payment_invoice_pdf_url TEXT;
ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS payment_invoice_status VARCHAR(30);
ALTER TABLE payment_orders ADD COLUMN IF NOT EXISTS billing_snapshot JSONB;
