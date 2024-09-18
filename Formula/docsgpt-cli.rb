class DocsgptCli < Formula
  desc "A CLI tool for DocsGPT"
  homepage "https://github.com/arc53/DocsGPT-cli"
  url "https://github.com/arc53/DocsGPT-cli/archive/refs/tags/v0.1.0.tar.gz"
  sha256 "2d1b0bef8ad54e33de8894ef6f5dbb5b73bcc40a93bb95b4b4b61ccb96428627"
  license "MIT"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args, "-o", bin/"docsgpt-cli"
  end

  test do
    assert_match "DocsGPT-cli version", shell_output("#{bin}/docsgpt-cli --version")
  end
end