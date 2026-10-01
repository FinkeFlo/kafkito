// Kafka's own topic-name rules (org.apache.kafka.common.internals.Topic):
// ASCII letters, digits, ".", "_" and "-", at most 249 characters, and
// neither "." nor "..". Checking them here gives an inline error before the
// broker rejects the request with InvalidTopicException.
const MAX_LENGTH = 249;
const LEGAL = /^[a-zA-Z0-9._-]+$/;

/** Returns the validation message for a topic name, or null when it is valid. */
export function topicNameError(raw: string): string | null {
  const name = raw.trim();
  if (name === "") return "Enter a topic name.";
  if (name === "." || name === "..") return 'A topic cannot be named "." or "..".';
  if (name.length > MAX_LENGTH) return `Use at most ${MAX_LENGTH} characters.`;
  if (!LEGAL.test(name)) return 'Only letters, digits, ".", "_" and "-" are allowed.';
  return null;
}
