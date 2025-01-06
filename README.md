# wayback_machine_downloaderGO

Wayback_machine_downloader, but written in Go

Based on [wayback_machine_downloader](https://github.com/hartator/wayback-machine-downloader)

## Builds

Just head over to the releases section of this repo and download a suited build for your system.
Builds (amd64):

- Windows
- Linux

Or download the source code and install Go to run it yourself.

## Usage

For builds:  
Windows:  
`./wayback_machine_downloader -url https://example.com/ --from 20241129171543 --to 20241129171545`  
Linux:  
`./wayback_machine_downloader -url https://example.com/ --from 20241129171543 --to 20241129171545`

For source code:  
`go run . -url https://example.com/ --from 20241129171543 --to 20241129171545`
