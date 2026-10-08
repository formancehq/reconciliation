<p align="center">
  <img src="https://formance01.b-cdn.net/Github-Attachements/banners/reconciliation-readme-banner.webp" alt="reconciliation" width="100%" />
</p>

# Formance Reconciliation

Formance Reconciliation continuously checks the invariants you define on your Formance ledgers: that two sets of accounts balance, that independent records of the same funds agree, or that a balance stays within bounds. When a check fails, it records the evidence and raises an alert that your team acknowledges, then resolves with a corrective booking or accepts with a note. Every evaluation and every alert transition is recorded in a Ledger v3 control ledger, so the history is auditable.

This branch is the Ledger v3 prototype. The design of reconciliation v3 is in [docs/](./docs/README.md) and in RFC 0022.


# Documentation

- [Documentation](https://docs.formance.com/modules/reconciliation)
- [API Reference](https://docs.formance.com/api-reference/introduction)