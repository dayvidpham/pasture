# Operations

## Prompt Example

```text
AskUserQuestion(questions: [...])
```

## Team Spawn

```text
TeamCreate({ team_name: "epoch-impl" })
SendMessage({ recipient: "worker-1" })
```

## Malformed Regression

```text
TeamCreate({...}) -> task(({...})
```
