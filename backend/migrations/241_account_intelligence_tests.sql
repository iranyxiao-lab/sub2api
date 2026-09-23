CREATE TABLE IF NOT EXISTS intelligence_questions (
    id BIGSERIAL PRIMARY KEY,
    title VARCHAR(160) NOT NULL,
    kind VARCHAR(20) NOT NULL CHECK (kind IN ('choice', 'short_answer', 'open')),
    prompt TEXT NOT NULL,
    choices JSONB NOT NULL DEFAULT '[]'::jsonb,
    answer TEXT NOT NULL DEFAULT '',
    rubric TEXT NOT NULL DEFAULT '',
    built_in BOOLEAN NOT NULL DEFAULT false,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO intelligence_questions (title, kind, prompt, answer, built_in)
SELECT '17 + 25', 'short_answer', 'What is 17 + 25? Reply with the number only.', '42', true
WHERE NOT EXISTS (SELECT 1 FROM intelligence_questions WHERE built_in = true AND title = '17 + 25');

INSERT INTO intelligence_questions (title, kind, prompt, choices, answer, built_in)
SELECT 'Set reasoning', 'choice', 'All squares are rectangles; no rectangles are circles. Can a square be a circle?',
       '["Yes", "No", "Not enough information"]'::jsonb, 'B', true
WHERE NOT EXISTS (SELECT 1 FROM intelligence_questions WHERE built_in = true AND title = 'Set reasoning');

INSERT INTO intelligence_questions (title, kind, prompt, rubric, built_in)
SELECT 'HTML table', 'open', 'Write a complete HTML document with a semantic table containing three rows of data and a caption. Include readable inline CSS.',
       'Review semantic markup, completeness, readability and visual clarity in the static preview.', true
WHERE NOT EXISTS (SELECT 1 FROM intelligence_questions WHERE built_in = true AND title = 'HTML table');

ALTER TABLE scheduled_test_plans
    ADD COLUMN IF NOT EXISTS test_kind VARCHAR(20) NOT NULL DEFAULT 'connectivity',
    ADD COLUMN IF NOT EXISTS question_ids BIGINT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS custom_prompt TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS question_cursor BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS claimed_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS claim_token VARCHAR(50);
CREATE UNIQUE INDEX IF NOT EXISTS idx_intelligence_plan_account
    ON scheduled_test_plans(account_id) WHERE test_kind = 'intelligence';

ALTER TABLE scheduled_test_results
    ADD COLUMN IF NOT EXISTS question_snapshot JSONB,
    ADD COLUMN IF NOT EXISTS prompt_snapshot TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS model_snapshot TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS score INT,
    ADD COLUMN IF NOT EXISTS grade_status VARCHAR(20),
    ADD COLUMN IF NOT EXISTS review_note TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS reviewed_by BIGINT,
    ADD COLUMN IF NOT EXISTS reviewed_at TIMESTAMPTZ;
