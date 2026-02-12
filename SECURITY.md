# Security Policy

## Reporting a Vulnerability

**Please do not open public GitHub issues for security vulnerabilities.**

Instead, email [support@fortunnels.ru](mailto:support@fortunnels.ru) with details of the issue.

### What to include

- Vulnerability type (e.g., XSS, SQL injection, etc.)
- Full path to the affected file
- Description of the issue
- Potential impact
- Steps to reproduce
- Suggested fix (if any)

### Process

1. We acknowledge receipt within 48 hours
2. We assess the issue and provide status within 7 days
3. We fix the vulnerability and ship a patch
4. We publish a security advisory after the fix

## Client security

### Encryption

- All connections to the server use HTTPS (TLS 1.2+)
- Optional stream encryption (PSK) is supported
- XChaCha20-Poly1305 is used for stream encryption

### Secret handling

- PSK is never written to disk
- Tokens and passwords can be provided via environment variables, files, or stdin
- Prefer environment variables, files, or stdin over CLI flags

### Recommendations

1. **Use long random PSKs:**

   ```bash
   ./bin/client 8000 -encrypt -psk "$(openssl rand -hex 32)"
   ```

2. **Store secrets in environment variables:**

   ```bash
   export FORTUNNELS_TOKEN="your-token"
   export FORTUNNELS_PSK="your-psk"
   ```

3. **Use files or stdin for secrets:**

   ```bash
   ./bin/client 8000 -token-file ./token.txt -psk-stdin
   ```

4. **Do not reuse a PSK across tunnels**

5. **Keep the client up to date** to receive the latest security fixes


## Acknowledgements

We thank everyone who responsibly reports security vulnerabilities.
