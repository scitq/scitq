from .__version__ import __version__

if __name__ == "__main__":
    import sys
    if len(sys.argv) > 1 and sys.argv[1] == "--version":
        print(__version__)