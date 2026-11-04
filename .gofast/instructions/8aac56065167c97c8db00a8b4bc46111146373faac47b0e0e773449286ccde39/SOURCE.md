# Vendored from mattpocock/skills

- Repository: https://github.com/mattpocock/skills
- Commit: 49dd158d1076134a641b33efb035946536778336 (2026-10-09)
- License: MIT (see LICENSE in this folder; keep it alongside the skills)
- Copied unchanged: skills/productivity/grilling, skills/engineering/domain-modeling (without agents/openai.yaml)
- grill-with-docs itself is not copied: it only loads these two, and it sets disable-model-invocation, which would stop the gofast grill stage from loading it.
- gofast-specific instructions live in plugin/skills/grill/SKILL.md, not here, so updating means re-copying at a newer commit.
