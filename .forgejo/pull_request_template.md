<!-- Development happens on code.overheid.nl. Codeberg is where most people
     will find the project and is where to take part today. A pull request
     here is welcome and will not be turned away for being in the wrong
     place - CONTRIBUTING.md describes how it travels back to canonical, and
     you need no account on code.overheid.nl for that to happen. -->

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
