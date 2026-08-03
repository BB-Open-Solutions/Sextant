<!-- Development happens on code.overheid.nl; this mirror is where most people
     will find the project. A pull request here is welcome - see
     CONTRIBUTING.md for how it travels back to canonical. -->

**What this changes, and why**
The reason matters more than the diff. A commit message that explains the
problem is worth more than one that describes the patch.

**How you know it works**
What you ran, and what it said. "Tests pass" is weaker than the output.

**Checked**
- [ ] `just ci` is green (fmt, vet, lint, tests, chart render, agent)
- [ ] New behaviour has a test that fails without the change - please verify
      that rather than assume it; a test that passes both ways is decoration
- [ ] Comments explain WHY where the reason is not obvious from the code

**Anything you are unsure about**
Say so here. A pull request with an open question is easier to review than one
that hides it.
