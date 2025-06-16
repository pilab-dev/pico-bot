# 🤖 Pico - The slack bot

---

The minimalist ai assistant for PiLab's slack channel. Free feel to fork/extend. Pull requests are welcome!

<small>Pico is available as a github action too! See the details bellow!</small>

<p align="center">
   <a href="https://github.com/pilab-dev/pico-bot"><img src="https://img.shields.io/github/stars/pilab-dev/pico-bot?style=social" alt="GitHub stars"></a>
   <a href="https://pkg.go.dev/github.com/pilab-dev/pico-bot"><img src="https://pkg.go.dev/badge/github.com/pilab-dev/pico-bot" alt="Go Reference"></a>
   <a href="https://github.com/pilab-dev/pico-bot/blob/main/LICENSE"><img src="https://img.shields.io/github/license/pilab-dev/pico-bot" alt="License"></a>
 </p>

## Activity

![Alt](https://repobeats.axiom.co/api/embed/f7673da7e66b7d699567c54e3fcaf684f0b6480b.svg "Repobeats analytics image")

This is a slack bot which lives in [pilab-hu.slack.com](https://pilab-hu.slack.com) domain. Can interact with our github repositories, connected to Google's Gemini using GenAI framework, to help our everyday work.

## 🚀 Getting Started

(We assumed that you already have go1.24 installed on your machine)

To run the bot without installing it, simply run:

```shell
go run github.com/pilab-dev/pico-bot@latest
```

## ⚙️ Configuration

To run the app, you should set the following variables:

```shell
export SLACK_BOT_TOKEN="xoxb-slack-bot-token"
export SLACK_CHANNEL_ID="C051SLACKCHANNEL"
export GITHUB_TOKEN="your-github-token"
export GITHUB_ORG="yourorg"
export GEMINI_API_KEY="gemini-api-key-from-ai-studio"
```

## 📋 TODO:

- [ ] finalize the github action
- [ ] build a Container for action
- [ ] implemnet `cobra-cli` based commands

---

# Summary action for Github Organization

This action summarizes and creates, then posts a message to slack from commit log from the latest activities.

## Inputs

## `slackToken`

**Required** The salack bot token to use by the bot. Default `"World"`.

## `slackChannel`

**Required** The slack channel where the pico will post the result.

## Outputs

## `time`

The time we greeted you.

## Example usage

```
uses: pilab-dev/pico-bot@v1
with:
  action: git-summary
```
