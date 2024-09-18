class DocsgptCli < Formula
  desc "A CLI tool for DocsGPT"
  homepage "https://github.com/arc53/DocsGPT-cli"
  url "file:///Users/pavel/Desktop/homebrew-tarballs/DocsGPT-cli-v1.0.0.tar.gz"
  sha256 "d5558cd419c8d46bdc958064cb97f963d1ea793866414c025906ec15033512ed"
  license "MIT"

  depends_on "go" => :build

  def install
    system "go", "build", *std_go_args, "-o", bin/"docsgpt-cli"
  end

  test do
    assert_match "DocsGPT-cli version", shell_output("#{bin}/docsgpt-cli --version")
  end
end