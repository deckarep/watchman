## Watchman

### Objective

This tool currently only targets `MacOS desktop` environments and acts as a background process which monitors a designated
folder that allows a user the ability to drag and drop files to a designated folder which causes a command to be executed 
for each file. This background process is meant to be very simple to use and tweak the behavior of the command so as not 
to incapacitate the user's computer.

### Developer Note

This tool is not yet stable or well documented and not yet production ready. Use at your own risk.


### Configuration

Here is a *sample configuration* for an imaginary command line tool called `oggify` that converts `.ogg` files to `.mp3`. 
Such a tool is not part of this repo but a tool that you would find suitable to be regularly used in your conversion 
workflow. Perhaps a tool such as `ffmpeg` or a tool that can convert `.doc` files to `.pdf` as an example.

```json
{
  "primary_interval": "30s",
  "max_workers": 3,
  "entries": [
    {
      "name": "oggify.mp3.to.ogg.converter",
      "interval": "2m",
      "command": "/usr/local/bin/oggify",
      "args": [
        "--in",
        "{SOURCE_FILE}",
        "--out",
        "{DEST_FILE}",
        "--additional-args"
      ],
      "folder": "~/Desktop/folder-to-watch/",
      "from_ext": ".mp3",
      "to_ext": ".ogg"
    }
  ]
}
```

### Usage

#### Installation

First, install Watchman using the usual Go toolchain commands.
```sh
go get github.com/deckarep/watchman
go build
```

Next, create your config: `config.json` file as needed and refer to the sample above.

Install the service using:
```sh
./Watchman install config/your_config.json 
```

If everything worked correctly, your service config file and the Watchman binary were copied here. The Watchman binary 
acts both a controller for you; the end user but is also responsible for running your service configs as needed:
```sh
/Users/{USER}/Library/Application Support/com.{USER}.scripts.go.watchman.svc
```

Additionally, the service should be running at this point:
```sh
./Watchman status

# Output:
# Command.status: "running" , Current PID: 60098, Last exit status: 0
```

To stop or remove the service:
```sh
# TODO
```

#### Troubleshooting:

Watchman can run locally while you are trying to work out your config file and conversion binaries. Just invoke it with
no arguments.
```sh
./Watchman
```

Tailing the service logs:
```
tail -f /tmp/com.{USER}.scripts.go.watchman.svc.log
```

### Ignore below, this tool will now manage the creation of launch configs.

Creating a MacOS launch config to manage this process
* Easy [Launchd Config Creator Here!](https://launched.zerowidth.com/)
* [Further reference](https://www.launchd.info/)