const connectionFieldAcronyms = new Set([
  "api",
  "arn",
  "aws",
  "ca",
  "db",
  "dsn",
  "gcp",
  "http",
  "https",
  "iam",
  "id",
  "jdbc",
  "json",
  "kms",
  "odbc",
  "pem",
  "s3",
  "sql",
  "ssh",
  "ssl",
  "sso",
  "tls",
  "uri",
  "url",
]);

// "ssl_mode" reads as "SSL mode". The config key stays visible beside it,
// because it is what the project config file and the docs use.
export function connectionFieldLabel(name: string) {
  const words = name
    .split(/[_\s]+/)
    .filter(Boolean)
    .map((word) =>
      connectionFieldAcronyms.has(word.toLowerCase()) ? word.toUpperCase() : word.toLowerCase(),
    );
  if (words.length === 0) return name;
  words[0] = words[0].charAt(0).toUpperCase() + words[0].slice(1);
  return words.join(" ");
}
