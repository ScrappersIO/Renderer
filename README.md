# Renderer

This program renders the JSON data produced by the [game engine](https://github.com/ScrappersIO/Game-Engine) into a GIF image.

    :> renderer.exe -help
    Usage of renderer.exe:
      -in string
            Specify the name of the JSON game file to be rendered. (default "scrappers.json")
      -out string
            Specify the name of the GIF to create. (default "scrappers.gif")
      -size int
            Specify the dimensions of the square output image. (default 600)
      -speed int
            Specify the GIF speed in ticks per second. (default 12)
      -threads int
            Specify the number of virtual threads to use while rendering. (default 8)
