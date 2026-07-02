INSERT INTO settings (key, value)
VALUES (
  'user_prompt_replacement_config',
  '{"enabled":true,"rules":[{"id":"environment-context-timezone-japan","name":"environment_context timezone -> Asia/Tokyo","enabled":true,"pattern":"(?s)(<environment_context\\b[^>]*>.*?<timezone>)([^<]*)(</timezone>.*?</environment_context>)","target_group":2,"replacement_type":"timezone_name","scope":"environment_context","timezone":"Asia/Tokyo"},{"id":"environment-context-current-date-japan","name":"environment_context current_date -> Asia/Tokyo today","enabled":true,"pattern":"(?s)(<environment_context\\b[^>]*>.*?<current_date>)([^<]*)(</current_date>.*?</environment_context>)","target_group":2,"replacement_type":"current_time","scope":"environment_context","timezone":"Asia/Tokyo","time_format":"2006-01-02"}]}'
)
ON CONFLICT (key) DO NOTHING;
